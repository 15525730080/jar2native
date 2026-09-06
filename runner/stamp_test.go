package runner

import (
	"bytes"
	"testing"
)

func TestStampRoundTrip(t *testing.T) {
	payload := []byte("payload")
	cfg := StampConfig{
		AppName:     "demo",
		JarName:     "demo.jar",
		JVMArgs:     []string{"-Xmx128m", "-Dname=value"},
		PayloadHash: "0123456789abcdef",
	}

	stamped, err := Stamp([]byte("runner"), payload, cfg)
	if err != nil {
		t.Fatal(err)
	}
	gotPayload, gotConfig, err := ParseTrailer(stamped)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotPayload, payload) {
		t.Fatalf("payload = %q, want %q", gotPayload, payload)
	}
	if gotConfig.AppName != cfg.AppName || gotConfig.JarName != cfg.JarName ||
		gotConfig.PayloadHash != cfg.PayloadHash || len(gotConfig.JVMArgs) != len(cfg.JVMArgs) {
		t.Fatalf("config = %#v, want %#v", gotConfig, cfg)
	}
}
