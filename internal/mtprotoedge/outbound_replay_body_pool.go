package mtprotoedge

// outboundReplayBodyPool owns the short-lived plaintext buffers used to
// materialize descriptor-backed rpc_result frames for an exact resend. It is
// deliberately bounded and Server-owned: a burst may warm the size classes,
// but it cannot make the process retain an unbounded sync.Pool tail.
type outboundReplayBodyPool struct {
	classes []outboundReplayBodyClass
}

type outboundReplayBodyClass struct {
	size int
	idle chan []byte
}

type outboundReplayBodyClassSpec struct {
	size    int
	maxIdle int
}

var defaultOutboundReplayBodyClasses = []outboundReplayBodyClassSpec{
	{size: 4<<10 + 64, maxIdle: 32},
	{size: 16<<10 + 64, maxIdle: 32},
	{size: 64<<10 + 64, maxIdle: 32},
	{size: 256<<10 + 64, maxIdle: 32},
	{size: 512<<10 + 64, maxIdle: 32},
	{size: 1<<20 + 64, maxIdle: 32},
	{size: 2<<20 + 64, maxIdle: 8},
}

func newOutboundReplayBodyPool(specs []outboundReplayBodyClassSpec) *outboundReplayBodyPool {
	classes := make([]outboundReplayBodyClass, 0, len(specs))
	for _, spec := range specs {
		if spec.size <= 0 || spec.maxIdle <= 0 {
			continue
		}
		classes = append(classes, outboundReplayBodyClass{
			size: spec.size,
			idle: make(chan []byte, spec.maxIdle),
		})
	}
	return &outboundReplayBodyPool{classes: classes}
}

// acquire returns an empty buffer and its owning class. Bodies larger than the
// largest reusable class use ordinary GC ownership and return class -1.
func (p *outboundReplayBodyPool) acquire(size int) ([]byte, int) {
	if p == nil || size <= 0 {
		return nil, -1
	}
	for class := range p.classes {
		bucket := &p.classes[class]
		if size > bucket.size {
			continue
		}
		select {
		case buf := <-bucket.idle:
			return buf[:0], class
		default:
			return make([]byte, 0, bucket.size), class
		}
	}
	return nil, -1
}

func (p *outboundReplayBodyPool) release(class int, buf []byte) {
	if p == nil || class < 0 || class >= len(p.classes) {
		return
	}
	bucket := &p.classes[class]
	if cap(buf) != bucket.size {
		return
	}
	buf = buf[:0]
	select {
	case bucket.idle <- buf:
	default:
	}
}

type outboundReplayBodyLease struct {
	pool  *outboundReplayBodyPool
	class int
	buf   []byte
}

func (l *outboundReplayBodyLease) release() {
	if l == nil || l.pool == nil {
		return
	}
	l.pool.release(l.class, l.buf)
	*l = outboundReplayBodyLease{}
}

var fallbackOutboundReplayBodyPool = newOutboundReplayBodyPool(defaultOutboundReplayBodyClasses)

func (c *Conn) replayBodyPool() *outboundReplayBodyPool {
	if c != nil && c.outboundReplayBodyPool != nil {
		return c.outboundReplayBodyPool
	}
	return fallbackOutboundReplayBodyPool
}
