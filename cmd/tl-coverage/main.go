// Command tl-coverage builds the Telegram API compatibility map of the server.
//
// It compares three sources:
//
//   - every TL method the protocol library knows (tlprofile, all supported
//     Layers), with the Layers each method exists in;
//   - the handlers the RPC router actually registers (a real *rpc.Router is
//     constructed and asked, so the list cannot drift from the code);
//   - a static read of internal/rpc that classifies each handler: a handler
//     whose every return is NOT_IMPLEMENTED is a stub, one that returns it on
//     some paths is partial. Returns guarded by "r.deps.X == nil" are ignored:
//     that only means an optional dependency is not wired.
//
// Decisions that cannot be derived from code -- "we will not do this, because"
// or a priority -- live in docs/compat/decisions.yaml and are merged in.
//
// Usage (from the repository root):
//
//	go run ./cmd/tl-coverage            # writes docs/COMPATIBILITY.md
//	go run ./cmd/tl-coverage -check     # fails if docs/COMPATIBILITY.md is stale
package main

import (
	"bytes"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/owpengram/owpengram-td/clock"
	"github.com/owpengram/owpengram-td/tg"
	"github.com/owpengram/owpengram-td/tlprofile"
	"go.uber.org/zap"
	"go.yaml.in/yaml/v3"

	"telesrv/internal/rpc"
)

const (
	tdModule = "github.com/owpengram/owpengram-td"
	minLayer = 225
)

type status string

const (
	statusDone    status = "done"
	statusPartial status = "partial"
	statusStatic  status = "static"
	statusStub    status = "stub"
	statusMissing status = "missing"
	statusSkip    status = "skip"
)

var statusOrder = []status{statusDone, statusStatic, statusPartial, statusStub, statusMissing, statusSkip}

var statusLabel = map[status]string{
	statusDone:    "✅ works",
	statusStatic:  "🔵 static",
	statusPartial: "🟡 partial",
	statusStub:    "🟠 stub",
	statusMissing: "❌ missing",
	statusSkip:    "⚪ not planned",
}

type method struct {
	Name     string // TL name, e.g. "messages.sendMessage"
	NS       string
	Layers   []int
	Status   status
	Handler  string // Go handler (onXxx) when known
	Priority string
	Note     string
}

type decision struct {
	Match    string `yaml:"match"`
	Status   string `yaml:"status"`   // skip | planned | edge
	Priority string `yaml:"priority"` // P1 | P2 | P3
	Note     string `yaml:"note"`
}

func main() {
	var (
		out       = flag.String("o", filepath.Join("docs", "COMPATIBILITY.md"), "output Markdown file")
		decisions = flag.String("decisions", filepath.Join("docs", "compat", "decisions.yaml"), "manual decisions file")
		rpcDir    = flag.String("rpc", filepath.Join("internal", "rpc"), "RPC handler package directory")
		check     = flag.Bool("check", false, "do not write; exit 1 if the output file is out of date")
	)
	flag.Parse()
	if err := run(*out, *decisions, *rpcDir, *check); err != nil {
		fmt.Fprintln(os.Stderr, "tl-coverage:", err)
		os.Exit(1)
	}
}

func run(out, decisionsPath, rpcDir string, check bool) error {
	ids, err := semanticMethodIDs()
	if err != nil {
		return err
	}
	router := rpc.New(rpc.Config{}, rpc.Deps{}, zap.NewNop(), clock.System)
	handlers, err := analyzeHandlers(rpcDir)
	if err != nil {
		return err
	}
	decs, err := loadDecisions(decisionsPath)
	if err != nil {
		return err
	}

	profiles := map[int]tlprofile.Profile{}
	for layer := minLayer; layer <= tg.Layer; layer++ {
		if p, ok := tlprofile.ResolveProfile(layer); ok {
			profiles[layer] = p
		}
	}

	var methods []method
	for _, id := range ids {
		category, name, ok := tlprofile.SemanticName(id)
		if !ok || category != "function" {
			continue
		}
		m := method{Name: name, NS: namespace(name)}
		for layer := minLayer; layer <= tg.Layer; layer++ {
			if p, ok := profiles[layer]; ok {
				if _, ok := tlprofile.WireID(p, id); ok {
					m.Layers = append(m.Layers, layer)
				}
			}
		}
		if len(m.Layers) == 0 {
			continue // overlay-only or retired before the supported window
		}
		h := handlers[name]
		switch {
		case !router.HandlesMethod(id):
			m.Status = statusMissing
		case h.kind == statusStub:
			m.Status = statusStub
		case h.kind == statusPartial:
			m.Status = statusPartial
		case h.static:
			m.Status = statusStatic
		default:
			m.Status = statusDone
		}
		m.Handler = h.handler
		if d, ok := matchDecision(decs, name); ok {
			m.Priority = d.Priority
			m.Note = d.Note
			switch {
			case d.Status == "skip" && m.Status != statusDone:
				m.Status = statusSkip
			case d.Status == "edge":
				// Consumed below the dispatcher (MTProto envelopes such as
				// invokeWithLayer): served, just not through registerRPC.
				m.Status = statusDone
				m.Handler = "(MTProto edge)"
			}
		}
		methods = append(methods, m)
	}
	sort.Slice(methods, func(i, j int) bool { return methods[i].Name < methods[j].Name })

	doc := render(methods)
	if check {
		current, err := os.ReadFile(out)
		if err != nil {
			return err
		}
		if !bytes.Equal(normalizeEOL(current), doc) {
			return fmt.Errorf("%s is out of date; run go run ./cmd/tl-coverage", out)
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(out, doc, 0o644); err != nil {
		return err
	}
	counts := map[status]int{}
	for _, m := range methods {
		counts[m.Status]++
	}
	fmt.Printf("%s: %d methods -- %d works, %d static, %d partial, %d stub, %d missing, %d not planned\n",
		out, len(methods), counts[statusDone], counts[statusStatic], counts[statusPartial], counts[statusStub], counts[statusMissing], counts[statusSkip])
	return nil
}

// semanticMethodIDs lists every SemanticMethod* constant of the protocol
// library. tlprofile has no enumeration API, so the generated metadata source
// of the exact module version in go.mod is read.
func semanticMethodIDs() ([]tlprofile.SemanticID, error) {
	dirOut, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", tdModule).Output()
	if err != nil {
		return nil, fmt.Errorf("locate %s: %w", tdModule, err)
	}
	path := filepath.Join(strings.TrimSpace(string(dirOut)), "tlprofile", "tl_profile_metadata_gen.go")
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	re := regexp.MustCompile(`(?m)^\s*SemanticMethod\w+\s+SemanticID = (0x[0-9a-f]+)`)
	var ids []tlprofile.SemanticID
	for _, m := range re.FindAllSubmatch(src, -1) {
		v, err := strconv.ParseUint(string(m[1]), 0, 64)
		if err != nil {
			return nil, err
		}
		ids = append(ids, tlprofile.SemanticID(v))
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("no SemanticMethod constants found in %s", path)
	}
	return ids, nil
}

type handlerInfo struct {
	handler string
	kind    status
	static  bool // never reads server state (r.deps), directly or through r.* calls
}

// analyzeHandlers parses the RPC package and classifies every registerRPC
// handler. The result is keyed by TL method name.
func analyzeHandlers(dir string) (map[string]handlerInfo, error) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, dir, func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		return nil, err
	}
	funcs := map[string]*ast.FuncDecl{}
	var files []*ast.File
	for _, pkg := range pkgs {
		for _, f := range pkg.Files {
			files = append(files, f)
			for _, decl := range f.Decls {
				if fd, ok := decl.(*ast.FuncDecl); ok && fd.Body != nil {
					funcs[fd.Name.Name] = fd
				}
			}
		}
	}

	out := map[string]handlerInfo{}
	depsMemo := map[string]bool{}
	for _, f := range files {
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || !isRegisterRPC(call.Fun) || len(call.Args) != 3 {
				return true
			}
			sel, ok := call.Args[1].(*ast.SelectorExpr)
			if !ok || !strings.HasPrefix(sel.Sel.Name, "SemanticMethod") {
				return true
			}
			id, ok := semanticByConstName(sel.Sel.Name)
			if !ok {
				return true
			}
			_, tlName, ok := tlprofile.SemanticName(id)
			if !ok {
				return true
			}
			lit, ok := call.Args[2].(*ast.FuncLit)
			if !ok {
				out[tlName] = handlerInfo{kind: statusDone}
				return true
			}
			info := handlerInfo{kind: classify(lit.Body, funcs, map[string]bool{})}
			info.static = !usesDeps(lit.Body, funcs, depsMemo)
			if name := firstRouterCall(lit.Body); name != "" {
				info.handler = name
			}
			out[tlName] = info
			return true
		})
	}
	return out, nil
}

var constIDs map[string]tlprofile.SemanticID

// semanticByConstName resolves a SemanticMethod* constant name to its value
// using the same generated source semanticMethodIDs reads.
func semanticByConstName(name string) (tlprofile.SemanticID, bool) {
	if constIDs == nil {
		constIDs = map[string]tlprofile.SemanticID{}
		dirOut, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", tdModule).Output()
		if err == nil {
			src, err := os.ReadFile(filepath.Join(strings.TrimSpace(string(dirOut)), "tlprofile", "tl_profile_metadata_gen.go"))
			if err == nil {
				re := regexp.MustCompile(`(?m)^\s*(SemanticMethod\w+)\s+SemanticID = (0x[0-9a-f]+)`)
				for _, m := range re.FindAllSubmatch(src, -1) {
					if v, err := strconv.ParseUint(string(m[2]), 0, 64); err == nil {
						constIDs[string(m[1])] = tlprofile.SemanticID(v)
					}
				}
			}
		}
	}
	id, ok := constIDs[name]
	return id, ok
}

// usesDeps reports whether a body reads r.deps, directly or through any
// r.<method> it calls (transitively, memoised per method name).
func usesDeps(body ast.Node, funcs map[string]*ast.FuncDecl, memo map[string]bool) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		if found {
			return false
		}
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		x, ok := sel.X.(*ast.Ident)
		if !ok || x.Name != "r" {
			return true
		}
		if sel.Sel.Name == "deps" {
			found = true
			return false
		}
		if fd, ok := funcs[sel.Sel.Name]; ok && fd.Recv != nil {
			name := sel.Sel.Name
			v, seen := memo[name]
			if !seen {
				memo[name] = false // break cycles
				v = usesDeps(fd.Body, funcs, memo)
				memo[name] = v
			}
			if v {
				found = true
				return false
			}
		}
		return true
	})
	return found
}

func isRegisterRPC(fun ast.Expr) bool {
	switch f := fun.(type) {
	case *ast.IndexExpr:
		id, ok := f.X.(*ast.Ident)
		return ok && id.Name == "registerRPC"
	case *ast.IndexListExpr:
		id, ok := f.X.(*ast.Ident)
		return ok && id.Name == "registerRPC"
	}
	return false
}

// firstRouterCall returns the first r.onXxx(...) the handler literal calls.
func firstRouterCall(body *ast.BlockStmt) string {
	var name string
	ast.Inspect(body, func(n ast.Node) bool {
		if name != "" {
			return false
		}
		if call, ok := n.(*ast.CallExpr); ok {
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok && strings.HasPrefix(sel.Sel.Name, "on") {
				name = sel.Sel.Name
			}
		}
		return true
	})
	return name
}

// classify decides whether a handler body really serves the method. It
// follows calls into r.onXxx methods so a thin registration literal is
// judged by the handler it forwards to.
func classify(body *ast.BlockStmt, funcs map[string]*ast.FuncDecl, seen map[string]bool) status {
	total, notImpl := 0, 0
	var follow []string
	var walk func(n ast.Node, guarded bool)
	walk = func(n ast.Node, guarded bool) {
		ast.Inspect(n, func(n ast.Node) bool {
			switch s := n.(type) {
			case *ast.FuncLit:
				return false // nested closures have their own returns
			case *ast.IfStmt:
				g := guarded || isDepsNilGuard(s.Cond)
				if s.Init != nil {
					walk(s.Init, guarded)
				}
				walk(s.Body, g)
				if s.Else != nil {
					walk(s.Else, guarded)
				}
				return false
			case *ast.ReturnStmt:
				if guarded {
					return false
				}
				if len(s.Results) == 0 {
					return false
				}
				if returnsNotImplemented(s) {
					total++
					notImpl++
					return false
				}
				// "return r.onXxx(ctx, req)" forwards the decision.
				if len(s.Results) == 1 {
					if call, ok := s.Results[0].(*ast.CallExpr); ok {
						if sel, ok := call.Fun.(*ast.SelectorExpr); ok && strings.HasPrefix(sel.Sel.Name, "on") {
							follow = append(follow, sel.Sel.Name)
							return false
						}
					}
				}
				total++
			}
			return true
		})
	}
	walk(body, false)

	for _, name := range follow {
		fd, ok := funcs[name]
		if !ok || seen[name] {
			total++
			continue
		}
		seen[name] = true
		switch classify(fd.Body, funcs, seen) {
		case statusStub:
			total++
			notImpl++
		case statusPartial:
			return statusPartial
		default:
			total++
		}
	}
	switch {
	case total > 0 && notImpl == total:
		return statusStub
	case notImpl > 0:
		return statusPartial
	default:
		return statusDone
	}
}

func returnsNotImplemented(s *ast.ReturnStmt) bool {
	for _, r := range s.Results {
		if call, ok := r.(*ast.CallExpr); ok {
			if id, ok := call.Fun.(*ast.Ident); ok && id.Name == "notImplementedErr" {
				return true
			}
		}
	}
	return false
}

// isDepsNilGuard recognises "r.deps.X == nil" (possibly inside || chains):
// such a branch means an optional dependency is not configured, not that the
// method is unimplemented.
func isDepsNilGuard(cond ast.Expr) bool {
	found := false
	ast.Inspect(cond, func(n ast.Node) bool {
		b, ok := n.(*ast.BinaryExpr)
		if !ok || b.Op != token.EQL {
			return true
		}
		if id, ok := b.Y.(*ast.Ident); !ok || id.Name != "nil" {
			return true
		}
		if sel, ok := b.X.(*ast.SelectorExpr); ok {
			if inner, ok := sel.X.(*ast.SelectorExpr); ok && inner.Sel.Name == "deps" {
				found = true
			}
		}
		return true
	})
	return found
}

func loadDecisions(path string) ([]decision, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []decision
	if err := yaml.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	for _, d := range out {
		if d.Match == "" {
			return nil, fmt.Errorf("%s: a decision without match", path)
		}
		switch d.Status {
		case "", "skip", "planned", "edge":
		default:
			return nil, fmt.Errorf("%s: %s: status must be skip, planned or edge, got %q", path, d.Match, d.Status)
		}
	}
	return out, nil
}

// matchDecision picks the most specific decision: an exact method beats a
// "namespace.*" pattern.
func matchDecision(decs []decision, name string) (decision, bool) {
	var best decision
	found := false
	for _, d := range decs {
		switch {
		case d.Match == name:
			return d, true
		case strings.HasSuffix(d.Match, ".*") && strings.HasPrefix(name, strings.TrimSuffix(d.Match, "*")):
			if !found {
				best, found = d, true
			}
		}
	}
	return best, found
}

func namespace(name string) string {
	if i := strings.IndexByte(name, '.'); i > 0 {
		return name[:i]
	}
	return "(core)"
}

func layersLabel(layers []int) string {
	if len(layers) == 0 {
		return ""
	}
	first, last := layers[0], layers[len(layers)-1]
	contiguous := last-first+1 == len(layers)
	switch {
	case first == minLayer && last == tg.Layer && contiguous:
		return "all"
	case last == tg.Layer && contiguous:
		return fmt.Sprintf("%d+", first)
	case contiguous:
		return fmt.Sprintf("%d–%d", first, last)
	}
	parts := make([]string, len(layers))
	for i, l := range layers {
		parts[i] = strconv.Itoa(l)
	}
	return strings.Join(parts, ",")
}

func render(methods []method) []byte {
	byNS := map[string][]method{}
	total := map[status]int{}
	for _, m := range methods {
		byNS[m.NS] = append(byNS[m.NS], m)
		total[m.Status]++
	}
	var nss []string
	for ns := range byNS {
		nss = append(nss, ns)
	}
	sort.Strings(nss)

	var b strings.Builder
	b.WriteString("# Telegram API compatibility\n\n")
	b.WriteString("<!-- Generated by `go run ./cmd/tl-coverage`. Do not edit by hand: change the code or docs/compat/decisions.yaml and regenerate. -->\n\n")
	fmt.Fprintf(&b, "Every TL method the server's protocol library knows for Layers %d–%d, and what the server does with it.\n\n", minLayer, tg.Layer)
	b.WriteString("| Status | Meaning |\n|---|---|\n")
	b.WriteString("| ✅ works | a handler is registered and serves the method |\n")
	b.WriteString("| 🔵 static | the handler never reads server state: a fixed or empty answer. Right for some methods, a placeholder for others |\n")
	b.WriteString("| 🟡 partial | the handler answers `NOT_IMPLEMENTED` on some paths (options or variants it does not support) |\n")
	b.WriteString("| 🟠 stub | a handler is registered but always answers `NOT_IMPLEMENTED` |\n")
	b.WriteString("| ❌ missing | no handler: the client gets `METHOD_NOT_FOUND`-style failure |\n")
	b.WriteString("| ⚪ not planned | missing or stubbed on purpose, see the note (docs/compat/decisions.yaml) |\n\n")

	fmt.Fprintf(&b, "**Total: %d methods** — ", len(methods))
	var parts []string
	for _, s := range statusOrder {
		parts = append(parts, fmt.Sprintf("%s %d", statusLabel[s], total[s]))
	}
	b.WriteString(strings.Join(parts, " · "))
	fmt.Fprintf(&b, " · served %d%%\n\n", percent(total[statusDone]+total[statusStatic]+total[statusPartial], len(methods)-total[statusSkip]))

	b.WriteString("## By namespace\n\n")
	b.WriteString("| Namespace | Methods | ✅ | 🔵 | 🟡 | 🟠 | ❌ | ⚪ | Served |\n|---|---:|---:|---:|---:|---:|---:|---:|---:|\n")
	for _, ns := range nss {
		c := map[status]int{}
		for _, m := range byNS[ns] {
			c[m.Status]++
		}
		n := len(byNS[ns])
		fmt.Fprintf(&b, "| [%s](#%s) | %d | %d | %d | %d | %d | %d | %d | %d%% |\n", ns, anchor(ns), n,
			c[statusDone], c[statusStatic], c[statusPartial], c[statusStub], c[statusMissing], c[statusSkip],
			percent(c[statusDone]+c[statusStatic]+c[statusPartial], n-c[statusSkip]))
	}
	b.WriteString("\n")

	b.WriteString("## To do\n\n")
	b.WriteString("Methods clients can call that the server does not fully serve, highest priority first ")
	b.WriteString("(static answers appear here once a decision gives them a priority).\n\n")
	var todo []method
	for _, m := range methods {
		// Static answers are listed only when a decision marks them as a
		// placeholder to replace (it carries a priority).
		if m.Status == statusPartial || m.Status == statusStub || m.Status == statusMissing ||
			(m.Status == statusStatic && m.Priority != "") {
			todo = append(todo, m)
		}
	}
	sort.SliceStable(todo, func(i, j int) bool {
		pi, pj := priorityRank(todo[i].Priority), priorityRank(todo[j].Priority)
		if pi != pj {
			return pi < pj
		}
		return todo[i].Name < todo[j].Name
	})
	b.WriteString("| Priority | Method | Status | Layers | Note |\n|---|---|---|---|---|\n")
	for _, m := range todo {
		fmt.Fprintf(&b, "| %s | `%s` | %s | %s | %s |\n", orDash(m.Priority), m.Name, statusLabel[m.Status], layersLabel(m.Layers), cell(m.Note))
	}
	b.WriteString("\n")

	b.WriteString("## All methods\n\n")
	for _, ns := range nss {
		fmt.Fprintf(&b, "### %s\n\n", ns)
		b.WriteString("| Method | Status | Layers | Handler | Note |\n|---|---|---|---|---|\n")
		for _, m := range byNS[ns] {
			fmt.Fprintf(&b, "| `%s` | %s | %s | %s | %s |\n", m.Name, statusLabel[m.Status], layersLabel(m.Layers), codeOrDash(m.Handler), cell(m.Note))
		}
		b.WriteString("\n")
	}
	return []byte(b.String())
}

func percent(n, d int) int {
	if d <= 0 {
		return 100
	}
	return n * 100 / d
}

func priorityRank(p string) int {
	switch p {
	case "P1":
		return 0
	case "P2":
		return 1
	case "P3":
		return 2
	}
	return 3
}

func anchor(ns string) string {
	return strings.NewReplacer("(", "", ")", "", ".", "").Replace(strings.ToLower(ns))
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func codeOrDash(s string) string {
	if s == "" {
		return "—"
	}
	return "`" + s + "`"
}

func cell(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "|", "\\|"), "\n", " ")
}

func normalizeEOL(b []byte) []byte { return bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n")) }
