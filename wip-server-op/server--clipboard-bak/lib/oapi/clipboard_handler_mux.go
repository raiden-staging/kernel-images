package oapi

import (
	"github.com/go-chi/chi/v5"
)

// HandlerFromMuxWithClipboard registers the clipboard handlers to the router
func HandlerFromMuxWithClipboard(si ExtendedServerInterface, r chi.Router) {
	// First register all the standard handlers
	HandlerFromMux(si, r)

	// Then register our clipboard handlers
	RegisterClipboardHandlers(r, si)
}
