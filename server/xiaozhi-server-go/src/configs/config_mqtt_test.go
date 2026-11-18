package configs

import "testing"

func TestCheckAndModifyConfigMQTTEnv(t *testing.T) {
	t.Setenv("MQTTUDP_ENABLED", "true")
	t.Setenv("MQTT_USERNAME", "u")
	t.Setenv("MQTT_PASSWORD", "p")
	cfg := CheckAndModifyConfig(&Config{})
	m := cfg.Transport.MQTTUDP
	if !m.Enabled || m.MQTT.Username != "u" || m.MQTT.Password != "p" {
		t.Fatalf("env not applied: %+v", m)
	}
}

func TestCheckAndModifyConfigMQTTEnvOverridesFalse(t *testing.T) {
	t.Setenv("MQTTUDP_ENABLED", "false")
	cfg := &Config{}
	cfg.Transport.MQTTUDP.Enabled = true
	if CheckAndModifyConfig(cfg).Transport.MQTTUDP.Enabled {
		t.Fatal("MQTTUDP_ENABLED=false must disable")
	}
}

func TestCheckAndModifyConfigMQTTEnvEmptyKeepsYAML(t *testing.T) {
	t.Setenv("MQTTUDP_ENABLED", "")
	t.Setenv("MQTT_USERNAME", "")
	t.Setenv("MQTT_PASSWORD", "")
	cfg := &Config{}
	cfg.Transport.MQTTUDP.Enabled = true
	cfg.Transport.MQTTUDP.MQTT.Username = "yu"
	cfg.Transport.MQTTUDP.MQTT.Password = "yp"
	m := CheckAndModifyConfig(cfg).Transport.MQTTUDP
	if !m.Enabled || m.MQTT.Username != "yu" || m.MQTT.Password != "yp" {
		t.Fatalf("yaml values must be kept: %+v", m)
	}
}
