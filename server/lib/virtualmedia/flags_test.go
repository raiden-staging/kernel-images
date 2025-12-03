package virtualmedia

import "testing"

func TestIsLegacyChromiumFlag(t *testing.T) {
	t.Parallel()

	cases := []struct {
		token string
		want  bool
	}{
		{"--use-fake-ui-for-media-stream", true},
		{"--use-fake-device-for-media-stream", true},
		{"--use-file-for-fake-video-capture=/tmp/file", true},
		{"--use-file-for-fake-audio-capture", true},
		{" --use-fake-ui-for-media-stream  ", true},
		{"--remote-debugging-port=9222", false},
		{"", false},
	}

	for _, tc := range cases {
		if got := IsLegacyChromiumFlag(tc.token); got != tc.want {
			t.Errorf("IsLegacyChromiumFlag(%q) = %t, want %t", tc.token, got, tc.want)
		}
	}
}

func TestFilterLegacyChromiumFlags(t *testing.T) {
	t.Parallel()

	input := []string{
		"--remote-debugging-port=9222",
		" --use-fake-ui-for-media-stream ",
		"--disable-gpu",
		"--use-file-for-fake-video-capture=/tmp/file",
		"  ",
	}
	got := FilterLegacyChromiumFlags(input)
	want := []string{"--remote-debugging-port=9222", "--disable-gpu"}

	if len(got) != len(want) {
		t.Fatalf("unexpected length: got %d want %d (values: %v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("unexpected token at %d: got %q want %q", i, got[i], want[i])
		}
	}
}
