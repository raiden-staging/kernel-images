package recording

import (
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"kernel-operator-api/internal/utils"
)

var (
	ffmpeg = func() string { if v:=os.Getenv("FFMPEG_BIN"); v!=""{return v}; return "/usr/bin/ffmpeg" }()
	display = func() string { if v:=os.Getenv("DISPLAY"); v!=""{return v}; return ":0" }()
)

type recState struct {
	proc       *exec.Cmd
	file       string
	startedAt  string
	finishedAt string
}

var (
	mu   sync.Mutex
	recs = map[string]*recState{} // id -> state
)

func buildArgsMp4(fps int, maxDur int, withAudio bool) []string {
	args := []string{"-nostdin", "-hide_banner", "-f", "x11grab", "-i", display+".0"}
	if withAudio {
		// best-effort default source
		args = append(args, "-f", "pulse", "-i", "default")
	}
	args = append(args, "-r", itoa(ifZero(fps, 20)), "-c:v", "libx264", "-preset", "veryfast", "-pix_fmt", "yuv420p", "-movflags", "+faststart")
	if maxDur > 0 {
		args = append(args, "-t", itoa(maxDur))
	}
	if withAudio {
		args = append(args, "-c:a", "aac", "-b:a", "128k", "-ac", "2", "-ar", "48000", "-shortest")
	}
	return args
}

func Start(id string, fps int, maxDur int) (string, string) {
	if id == "" {
		id = "default"
	}
	mu.Lock()
	defer mu.Unlock()
	if recs[id] != nil && recs[id].proc != nil {
		panic("Already recording")
	}
	file := filepath.Join(utils.RecordingsDir, id+"-"+itoa(int(time.Now().UnixMilli()))+".mp4")
	args := append([]string{"-y"}, buildArgsMp4(fps, maxDur, true)...)
	args = append(args, file)
	cmd := utils.Spawn(ffmpeg, args, nil, "")
	_ = cmd.Start()
	rs := &recState{proc: cmd, file: file, startedAt: time.Now().UTC().Format(time.RFC3339)}
	recs[id] = rs
	go func() {
		_ = cmd.Wait()
		mu.Lock()
		if recs[id] != nil {
			recs[id].finishedAt = time.Now().UTC().Format(time.RFC3339)
			recs[id].proc = nil
		}
		mu.Unlock()
	}()
	return id, rs.startedAt
}

func Stop(id string, force bool) bool {
	if id == "" {
		id = "default"
	}
	mu.Lock()
	defer mu.Unlock()
	rs := recs[id]
	if rs == nil || rs.proc == nil {
		panic("Not recording")
	}
	if force {
		_ = rs.proc.Process.Kill()
	} else {
		_ = rs.proc.Process.Signal(os.Interrupt)
	}
	return true
}

func InfoList() []map[string]any {
	mu.Lock(); defer mu.Unlock()
	if len(recs) == 0 {
		return []map[string]any{{"id":"default", "isRecording": false, "started_at": nil, "finished_at": nil}}
	}
	var out []map[string]any
	for id, r := range recs {
		out = append(out, map[string]any{
			"id": id, "isRecording": r.proc != nil, "started_at": r.startedAt, "finished_at": r.finishedAt,
		})
	}
	return out
}

func LatestFilePath(id string) string {
	if id == "" { id = "default" }
	mu.Lock(); defer mu.Unlock()
	rs := recs[id]
	if rs == nil { return "" }
	return rs.file
}

func IsRecording(id string) bool {
	if id == "" { id = "default" }
	mu.Lock(); defer mu.Unlock()
	return recs[id] != nil && recs[id].proc != nil
}

func Delete(id string) bool {
	if id == "" { id = "default" }
	mu.Lock(); defer mu.Unlock()
	rs := recs[id]
	if rs == nil || rs.file == "" {
		panic("Not found")
	}
	_ = os.Remove(rs.file)
	return true
}

func ifZero(v, def int) int { if v==0 { return def }; return v }
func itoa(i int) string { return strconvI(i) }
func strconvI(i int) string {
	if i==0 { return "0" }
	sign := ""
	if i<0 { sign="-"; i=-i }
	b := []byte{}
	for i>0 {
		b = append([]byte{byte('0'+i%10)}, b...)
		i/=10
	}
	return sign+string(b)
}