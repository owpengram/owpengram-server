package web

import (
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"go.uber.org/zap"
)

// The public root ("/") is this server's own page: its name, icon and
// description, and an "Add to <app>" button carrying the owpg://addserver
// link. It is what the admin panel's Share button hands out, as plain https:
// an owpg:// link is invisible to most apps and QR scanners, while an https
// one opens anywhere and then offers the app.
//
// The add-server link carries host and port only -- the client fetches the
// name, key and DC from whatever answers at that address (see
// cmd/telesrv-admin/addserverlink.go for why nothing else may go in it).

// addServerAppURL is the owpg://addserver link for this server, or "" when no
// public address is configured.
func (h *handler) addServerAppURL() string {
	if h.advertiseHost == "" || h.serverPort <= 0 {
		return ""
	}
	q := url.Values{}
	q.Set("host", h.advertiseHost)
	q.Set("port", strconv.Itoa(h.serverPort))
	return h.appScheme + "://addserver?" + q.Encode()
}

func (h *handler) serverLanding(w http.ResponseWriter, r *http.Request) {
	name := h.appName
	description := ""
	hasIcon := false
	if h.identity != nil {
		if info, err := h.identity.Get(); err == nil {
			if strings.TrimSpace(info.Name) != "" {
				name = strings.TrimSpace(info.Name)
			}
			description = strings.TrimSpace(info.Description)
			hasIcon = info.IconExt != ""
		} else {
			h.logger.Warn("Read server identity for the public page failed", zap.Error(err))
		}
	}
	about := description
	if description == "" {
		description = "A " + h.appName + " server. Add it to the app to sign in or create an account."
	}

	data := usernamePageData{
		AppName:      h.appName,
		AppInitial:   appInitial(h.appName),
		Title:        name,
		Extra:        h.serverAddressLabel(),
		Description:  description,
		AboutText:    about,
		CanonicalURL: h.publicBaseURL + "/",
		HomeURL:      h.publicBaseURL + "/",
		DownloadURL:  h.downloadURL,
		ButtonLabel:  "Add to " + h.appName,
		Initials:     appInitial(name),
		GradFrom:     "#38bdf8",
		GradTo:       "#2563eb",
	}
	if app := h.addServerAppURL(); app != "" {
		data.AppURL = template.URL(app)
	} else {
		// Without an advertised address there is nothing to add; the button
		// falls back to the download page rather than a dead link.
		data.AppURL = template.URL(h.downloadURL)
		data.ButtonLabel = "Get " + h.appName
	}
	if h.webBaseURL != "" {
		data.WebURL = template.URL(strings.TrimRight(h.webBaseURL, "/") + "/")
	}
	if hasIcon {
		data.PhotoURL = h.publicBaseURL + "/_public/server-icon"
	} else {
		data.PhotoURL = h.publicBaseURL + "/_public/assets/logo.png"
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=60, must-revalidate")
	if err := usernameLandingTemplate.Execute(w, data); err != nil {
		h.logger.Error("Render public server page failed", zap.Error(err))
	}
}

// serverAddressLabel is the "host:port" line under the name, the same
// address a user would type into the app's Add Server form by hand.
func (h *handler) serverAddressLabel() string {
	if h.advertiseHost == "" || h.serverPort <= 0 {
		return ""
	}
	if strings.Contains(h.advertiseHost, ":") {
		return "[" + h.advertiseHost + "]:" + strconv.Itoa(h.serverPort)
	}
	return h.advertiseHost + ":" + strconv.Itoa(h.serverPort)
}

var serverIconTypes = map[string]string{
	"png":  "image/png",
	"jpg":  "image/jpeg",
	"jpeg": "image/jpeg",
	"webp": "image/webp",
	"gif":  "image/gif",
}

// serverIcon serves the operator-uploaded server icon, or the brand logo when
// none is set.
func (h *handler) serverIcon(w http.ResponseWriter, r *http.Request) {
	if h.identity != nil {
		if data, ext, ok := h.identity.Icon(); ok {
			// Raster types only, by an explicit list: the admin upload
			// accepts nothing else, and this keeps it so even if a file got
			// there another way -- an SVG here would be script on this origin.
			contentType, allowed := serverIconTypes[strings.ToLower(strings.TrimPrefix(ext, "."))]
			if !allowed {
				h.brandLogo(w, r)
				return
			}
			w.Header().Set("Content-Type", contentType)
			w.Header().Set("Cache-Control", "public, max-age=300")
			w.Header().Set("X-Content-Type-Options", "nosniff")
			_, _ = w.Write(data)
			return
		}
	}
	h.brandLogo(w, r)
}
