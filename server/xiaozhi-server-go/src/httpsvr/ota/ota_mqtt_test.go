package ota

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"xiaozhi-server-go/src/configs"

	"github.com/gin-gonic/gin"
)

func TestOTAMQTTWhenEnabled(t *testing.T) {
	setupPrivacyTest(t)
	m := &configs.Cfg.Transport.MQTTUDP
	m.Enabled = true
	m.MQTT.IP = "192.168.1.10"
	m.MQTT.Port = 1883
	m.MQTT.Username = "u"
	m.MQTT.Password = "p"

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest("POST", "/api/ota/", strings.NewReader(privacyTestBody))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Header.Set("device-id", "aa:bb:cc:dd:ee:05")
	c.Request.Header.Set("client-id", "cid")
	(&DefaultOTAService{}).HandleOTARequest()(c)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d", recorder.Code)
	}
	var out map[string]map[string]interface{}
	if err := json.Unmarshal(recorder.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	mq := out["mqtt"]
	want := map[string]string{
		"endpoint":      "192.168.1.10:1883",
		"client_id":     "CGID_test@@@aa_bb_cc_dd_ee_05@@@cid",
		"username":      "u",
		"password":      "p",
		"publish_topic": "device-server",
	}
	for k, v := range want {
		if mq[k] != v {
			t.Fatalf("mqtt.%s = %v, want %v (body=%s)", k, mq[k], v, recorder.Body.String())
		}
	}
	if v, ok := mq["subscribe_topic"]; !ok || v != nil {
		t.Fatalf("subscribe_topic must be present and null: %s", recorder.Body.String())
	}
	if out["websocket"]["url"] == nil {
		t.Fatalf("websocket.url missing: %s", recorder.Body.String())
	}
}
