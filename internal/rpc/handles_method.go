package rpc

import "github.com/owpengram/owpengram-td/tlprofile"

// HandlesMethod reports whether the router registered a handler for the TL
// method. It exists for cmd/tl-coverage, which builds the Telegram
// compatibility map from the live dispatcher rather than from source text.
func (r *Router) HandlesMethod(method tlprofile.SemanticID) bool {
	return r != nil && r.dispatcher.Has(method)
}
