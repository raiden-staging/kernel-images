package screenshot

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"

	"kernel-operator-api/internal/utils/env"
	"kernel-operator-api/internal/utils/ids"
)

func Register(mux *http.ServeMux) {
	mux.HandleFunc("/screenshot/capture", capture)
	mux.HandleFunc("/screenshot/", getByID) // /screenshot/{id}
}

type captureReq struct {
	Region        *struct{ X, Y, Width, Height int } `json:"region"`
	IncludeCursor bool                                `json:"include_cursor"`
	Display       int                                 `json:"display"`
	Format        string                              `json:"format"`
	Quality       int                                 `json:"quality"`
}

func capture(w http.ResponseWriter, r *http.Request) {
	var req captureReq
	_ = json.NewDecoder(r.Body).Decode(&req)
	id := ids.New()
	ext := "png"
	if req.Format == "jpeg" || req.Format == "jpg" {
		ext = "jpg"
	}
	outPath := filepath.Join(env.ScreensDir, id+"."+ext)
	ok := false

	if have("grim") {
		args := []string{}
		if req.Region != nil {
			args = append(args, "-g", formatRegion(req.Region))
		}
		args = append(args, outPath)
		if err := exec.Command("grim", args...).Run(); err == nil {
			ok = true
		}
	}

	if !ok && have(firstNonEmpty(os.Getenv("FFMPEG_BIN"), "ffmpeg")) {
		display := firstNonEmpty(os.Getenv("DISPLAY"), ":0") + ".0"
		args := []string{"-y", "-f", "x11grab", "-i", display, "-vframes", "1"}
		if req.Region != nil {
			args = append(args, "-vf", "crop="+cropExpr(req.Region))
		}
		args = append(args, outPath)
		if err := exec.Command(firstNonEmpty(os.Getenv("FFMPEG_BIN"), "ffmpeg"), args...).Run(); err == nil {
			ok = true
		}
	}

	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"message": "Capture failed"})
		return
	}

	b, err := os.ReadFile(outPath)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"message": err.Error()})
		return
	}
	ct := "image/png"
	if ext == "jpg" {
		ct = "image/jpeg"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"screenshot_id": id,
		"content_type":  ct,
		"bytes_b64":     base64.StdEncoding.EncodeToString(b),
	})
}

func getByID(w http.ResponseWriter, r *http.Request) {
	id := filepath.Base(r.URL.Path)
	if id == "" || id == "screenshot" {
		writeJSON(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
		return
	}
	png := filepath.Join(env.ScreensDir, id+".png")
	jpg := filepath.Join(env.ScreensDir, id+".jpg")
	var f string
	if fileExists(png) {
		f = png
	} else if fileExists(jpg) {
		f = jpg
	} else {
		writeJSON(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
		return
	}
	data, _ := os.ReadFile(f)
	if filepath.Ext(f) == ".jpg" {
		w.Header().Set("Content-Type", "image/jpeg")
	} else {
		w.Header().Set("Content-Type", "image/png")
	}
	_, _ = w.Write(data)
}

func have(bin string) bool {
	_, err := exec.LookPath(bin)
	return err == nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func formatRegion(r *struct{ X, Y, Width, Height int }) string {
	return strconv.Itoa(r.X) + "," + strconv.Itoa(r.Y) + " " + strconv.Itoa(r.Width) + "x" + strconv.Itoa(r.Height)
}

func cropExpr(r *struct{ X, Y, Width, Height int }) string {
	return fmt.Sprintf("%d:%d:%d:%d", r.Width, r.Height, r.X, r.Y)
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return !errors.Is(err, os.ErrNotExist)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}