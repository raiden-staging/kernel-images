package streamsvc

import (
	"errors"
	"os"
	"os/exec"
	"sync"

	"kernel-operator-api/internal/utils"
)

var (
	ffmpeg = func() string { if v:=os.Getenv("FFMPEG_BIN"); v!=""{return v}; return "/usr/bin/ffmpeg" }()
	display = func() string { if v:=os.Getenv("DISPLAY"); v!=""{return v}; return ":0" }()
	pulseSource = func() string { if v:=os.Getenv("PULSE_SOURCE"); v!=""{return v}; return "default" }()
)

type streamItem struct {
	proc *exec.Cmd
	metrics chan map[string]any
}

var (
	mu sync.Mutex
	streams = map[string]*streamItem{}
)

func Start(rtmpsURL, streamKey string, fps int, region *struct{X,Y,Width,Height int}, capAudio bool) (string, error) {
	if rtmpsURL=="" || streamKey=="" {
		return "", errors.New("Missing RTMPS params")
	}
	id := utils.UID()
	input := []string{"-f","x11grab","-r",itoa(ifZero(fps,30)),"-i",display+".0"}
	audio := []string{}
	if capAudio {
		audio = []string{"-f","pulse","-i",pulseSource}
	}
	vf := []string{}
	if region!=nil {
		vf = []string{"-filter:v", "crop="+itoa(region.Width)+":"+itoa(region.Height)+":"+itoa(region.X)+":"+itoa(region.Y)}
	}
	out := []string{"-c:v","libx264","-b:v","3500k","-c:a","aac","-f","flv",rtmpsURL+"/"+streamKey}
	args := append([]string{"-hide_banner","-thread_queue_size","512"}, append(append(append(input, audio...), vf...), out...)...)
	cmd := utils.Spawn(ffmpeg, args, nil, "")
	stderr, _ := cmd.StderrPipe()
	_ = cmd.Start()

	it := &streamItem{proc: cmd, metrics: make(chan map[string]any, 16)}
	mu.Lock(); streams[id] = it; mu.Unlock()

	// very rough parse: forward stderr lines as-is
	go func() {
		br := bufio.NewReader(stderr)
		for {
			line, err := br.ReadString('\n')
			if len(line)>0 {
				it.metrics <- map[string]any{"ts": time.Now().UTC().Format(time.RFC3339), "log": line}
			}
			if err != nil { break }
		}
		close(it.metrics)
	}()

	return id, nil
}

func Stop(id string) error {
	mu.Lock(); it := streams[id]; delete(streams, id); mu.Unlock()
	if it == nil { return errors.New("Not Found") }
	_ = it.proc.Process.Signal(os.Interrupt)
	return nil
}

func MetricsEmitter(id string) <-chan map[string]any {
	mu.Lock(); it := streams[id]; mu.Unlock()
	if it == nil { return nil }
	return it.metrics
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

import (
	"bufio"
	"time"
)