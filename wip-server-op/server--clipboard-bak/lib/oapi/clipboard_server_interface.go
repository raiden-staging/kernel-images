package oapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
)

// SetClipboardJSONRequestBody defines body for SetClipboard for application/json ContentType.
type SetClipboardJSONRequestBody = ClipboardContent

// ExtendedServerInterface extends the ServerInterface with clipboard methods
type ExtendedServerInterface interface {
	ServerInterface

	// Get clipboard content
	// (GET /clipboard)
	GetClipboard(w http.ResponseWriter, r *http.Request)

	// Set clipboard content
	// (POST /clipboard)
	SetClipboard(w http.ResponseWriter, r *http.Request)

	// Stream clipboard events
	// (GET /clipboard/stream)
	StreamClipboard(w http.ResponseWriter, r *http.Request)
}

// ExtendedServerInterfaceWrapper extends the ServerInterfaceWrapper with clipboard handlers
type ExtendedServerInterfaceWrapper struct {
	ServerInterfaceWrapper
	Handler ExtendedServerInterface
}

// GetClipboard operation middleware
func (siw *ExtendedServerInterfaceWrapper) GetClipboard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var handler http.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		siw.Handler.GetClipboard(w, r)
	})

	for _, middleware := range siw.HandlerMiddlewares {
		handler = middleware(handler)
	}

	handler.ServeHTTP(w, r.WithContext(ctx))
}

// SetClipboard operation middleware
func (siw *ExtendedServerInterfaceWrapper) SetClipboard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var handler http.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		siw.Handler.SetClipboard(w, r)
	})

	for _, middleware := range siw.HandlerMiddlewares {
		handler = middleware(handler)
	}

	handler.ServeHTTP(w, r.WithContext(ctx))
}

// StreamClipboard operation middleware
func (siw *ExtendedServerInterfaceWrapper) StreamClipboard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var handler http.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		siw.Handler.StreamClipboard(w, r)
	})

	for _, middleware := range siw.HandlerMiddlewares {
		handler = middleware(handler)
	}

	handler.ServeHTTP(w, r.WithContext(ctx))
}

// RegisterHandlers adds handlers for the clipboard operations
func RegisterClipboardHandlers(router chi.Router, si ExtendedServerInterface) {
	wrapper := ExtendedServerInterfaceWrapper{
		ServerInterfaceWrapper: ServerInterfaceWrapper{
			Handler:            si,
			HandlerMiddlewares: nil,
			ErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {
				http.Error(w, err.Error(), http.StatusBadRequest)
			},
		},
		Handler: si,
	}

	router.Get("/clipboard", wrapper.GetClipboard)
	router.Post("/clipboard", wrapper.SetClipboard)
	router.Get("/clipboard/stream", wrapper.StreamClipboard)
}

// Implementation methods for strictHandler
func (sh *strictHandler) GetClipboard(w http.ResponseWriter, r *http.Request) {
	var request GetClipboardRequestObject

	handler := func(ctx context.Context, w http.ResponseWriter, r *http.Request, request interface{}) (interface{}, error) {
		return sh.ssi.GetClipboard(ctx, request.(GetClipboardRequestObject))
	}
	for _, middleware := range sh.middlewares {
		handler = middleware(handler, "GetClipboard")
	}

	response, err := handler(r.Context(), w, r, request)

	if err != nil {
		sh.options.ResponseErrorHandlerFunc(w, r, err)
	} else if validResponse, ok := response.(GetClipboardResponseObject); ok {
		if err := validResponse.visitGetClipboardResponse(w); err != nil {
			sh.options.ResponseErrorHandlerFunc(w, r, err)
		}
	} else if response != nil {
		sh.options.ResponseErrorHandlerFunc(w, r, fmt.Errorf("Unexpected response type: %T", response))
	}
}

func (sh *strictHandler) SetClipboard(w http.ResponseWriter, r *http.Request) {
	var request SetClipboardRequestObject

	var body SetClipboardJSONRequestBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		sh.options.RequestErrorHandlerFunc(w, r, fmt.Errorf("can't decode JSON body: %w", err))
		return
	}
	request.Body = body

	handler := func(ctx context.Context, w http.ResponseWriter, r *http.Request, request interface{}) (interface{}, error) {
		return sh.ssi.SetClipboard(ctx, request.(SetClipboardRequestObject))
	}
	for _, middleware := range sh.middlewares {
		handler = middleware(handler, "SetClipboard")
	}

	response, err := handler(r.Context(), w, r, request)

	if err != nil {
		sh.options.ResponseErrorHandlerFunc(w, r, err)
	} else if validResponse, ok := response.(SetClipboardResponseObject); ok {
		if err := validResponse.visitSetClipboardResponse(w); err != nil {
			sh.options.ResponseErrorHandlerFunc(w, r, err)
		}
	} else if response != nil {
		sh.options.ResponseErrorHandlerFunc(w, r, fmt.Errorf("Unexpected response type: %T", response))
	}
}

func (sh *strictHandler) StreamClipboard(w http.ResponseWriter, r *http.Request) {
	var request StreamClipboardRequestObject

	handler := func(ctx context.Context, w http.ResponseWriter, r *http.Request, request interface{}) (interface{}, error) {
		return sh.ssi.StreamClipboard(ctx, request.(StreamClipboardRequestObject))
	}
	for _, middleware := range sh.middlewares {
		handler = middleware(handler, "StreamClipboard")
	}

	response, err := handler(r.Context(), w, r, request)

	if err != nil {
		sh.options.ResponseErrorHandlerFunc(w, r, err)
	} else if validResponse, ok := response.(StreamClipboardResponseObject); ok {
		if err := validResponse.visitStreamClipboardResponse(w); err != nil {
			sh.options.ResponseErrorHandlerFunc(w, r, err)
		}
	} else if response != nil {
		sh.options.ResponseErrorHandlerFunc(w, r, fmt.Errorf("Unexpected response type: %T", response))
	}
}
