package config

import "testing"

func TestFromEnv(t *testing.T) {
	t.Setenv("RELAY_LISTEN_ADDR", "127.0.0.1:9090")
	t.Setenv("RELAY_MAX_MESSAGE_BYTES", "524288")
	t.Setenv("RELAY_PING_INTERVAL", "5s")
	config, err := FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if config.ListenAddr != "127.0.0.1:9090" || config.MaxMessageBytes != 524288 || config.PingInterval.String() != "5s" {
		t.Fatalf("config = %#v", config)
	}
}

func TestFromEnvRejectsInvalidValue(t *testing.T) {
	t.Setenv("RELAY_WRITE_QUEUE_SIZE", "0")
	if _, err := FromEnv(); err == nil {
		t.Fatal("FromEnv() accepted zero queue size")
	}
}
