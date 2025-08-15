package app

import (
	"github.com/go-chi/chi/v5"

	"kernel-operator-api/internal/routes/bus"
	"kernel-operator-api/internal/routes/clipboard"
	"kernel-operator-api/internal/routes/fs"
	"kernel-operator-api/internal/routes/health"
	"kernel-operator-api/internal/routes/input"
	"kernel-operator-api/internal/routes/logs"
	"kernel-operator-api/internal/routes/macros"
	"kernel-operator-api/internal/routes/metrics"
	"kernel-operator-api/internal/routes/network"
	"kernel-operator-api/internal/routes/osinfo"
	"kernel-operator-api/internal/routes/pipe"
	"kernel-operator-api/internal/routes/proc"
	"kernel-operator-api/internal/routes/recording"
	"kernel-operator-api/internal/routes/screenshot"
	"kernel-operator-api/internal/routes/scripts"
	"kernel-operator-api/internal/routes/stream"
	"kernel-operator-api/internal/routes/browser"
	"kernel-operator-api/internal/routes/browserext"
)

func Router() *chi.Mux {
	r := chi.NewRouter()

	r.Mount("/", recording.Router())
	r.Mount("/", fs.Router())
	r.Mount("/", screenshot.Router())
	r.Mount("/", stream.Router())
	r.Mount("/", input.Router())
	r.Mount("/", proc.Router())
	r.Mount("/", network.Router())
	r.Mount("/", bus.Router())
	r.Mount("/", logs.Router())
	r.Mount("/", clipboard.Router())
	r.Mount("/", metrics.Router())
	r.Mount("/", macros.Router())
	r.Mount("/", scripts.Router())
	r.Mount("/", osinfo.Router())
	r.Mount("/", browser.Router())
	r.Mount("/", pipe.Router())
	r.Mount("/", health.Router())
	r.Mount("/", browserext.Router())

	return r
}