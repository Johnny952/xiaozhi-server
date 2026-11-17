package ota

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"xiaozhi-server-go/src/configs"
	"xiaozhi-server-go/src/configs/database"
	"xiaozhi-server-go/src/core/utils"
	"xiaozhi-server-go/src/models"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

const privacyTestBody = `{"application":{"version":"2.4.2"},"board":{"type":"esp32-muma-a"},"partition_table":[{"label":"ota_0"}]}`

// setupPrivacyTest prepares an in-memory DB, a file logger and a minimal config.
// It returns the log file path.
func setupPrivacyTest(t *testing.T) string {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.Agent{}, &models.Device{}); err != nil {
		t.Fatal(err)
	}
	previousDB := database.DB
	database.DB = db
	t.Cleanup(func() { database.DB = previousDB })

	logDir := t.TempDir()
	previousLogger := utils.DefaultLogger
	logger, err := utils.NewLogger(&utils.LogCfg{LogDir: logDir, LogFile: "ota-test.log", LogLevel: "debug"})
	if err != nil {
		t.Fatal(err)
	}
	utils.DefaultLogger = logger
	t.Cleanup(func() {
		_ = logger.Close()
		utils.DefaultLogger = previousLogger
	})

	previousCfg := configs.Cfg
	configs.Cfg = &configs.Config{
		SelectedModule: map[string]string{"LLM": "test-llm", "TTS": "test-tts"},
		LLM:            map[string]configs.LLMConfig{"test-llm": {}},
		TTS:            map[string]configs.TTSConfig{"test-tts": {Voice: "test-voice"}},
	}
	configs.Cfg.Web.Websocket = "ws://example.test/xiaozhi/v1/"
	t.Cleanup(func() { configs.Cfg = previousCfg })
	return filepath.Join(logDir, "ota-test.log")
}

func postOTA(t *testing.T, deviceID, body string) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest("POST", "/api/ota/", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	if deviceID != "" {
		c.Request.Header.Set("device-id", deviceID)
	}
	(&DefaultOTAService{}).HandleOTARequest()(c)
	return recorder
}

func TestOTALogsRawBody(t *testing.T) {
	logPath := setupPrivacyTest(t)
	rec := postOTA(t, "aa:bb:cc:dd:ee:01", privacyTestBody)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	log := string(data)
	if !strings.Contains(log, "esp32-muma-a") || !strings.Contains(log, "partition_table") {
		t.Fatalf("raw body not logged: %s", log)
	}
}

func TestOTANeverOffersFirmwareEvenWithBin(t *testing.T) {
	setupPrivacyTest(t)
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.MkdirAll("ota_bin", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join("ota_bin", "9.9.9.bin"), []byte("fw"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec := postOTA(t, "aa:bb:cc:dd:ee:02", privacyTestBody)
	var resp OtaFirmwareResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Firmware.URL != "" || resp.Firmware.Version != "2.4.2" {
		t.Fatalf("firmware = %+v", resp.Firmware)
	}
	if resp.Websocket.URL != "ws://example.test/xiaozhi/v1/" {
		t.Fatalf("websocket = %q", resp.Websocket.URL)
	}
}

func TestOTATimezoneChile(t *testing.T) {
	setupPrivacyTest(t)
	rec := postOTA(t, "aa:bb:cc:dd:ee:03", privacyTestBody)
	var resp OtaFirmwareResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.ServerTime.TimezoneOffset != -180 {
		t.Fatalf("timezone_offset = %d", resp.ServerTime.TimezoneOffset)
	}
}

func TestOTANoActivationNoMQTT(t *testing.T) {
	setupPrivacyTest(t)
	rec := postOTA(t, "aa:bb:cc:dd:ee:04", privacyTestBody)
	var m map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"activation", "mqtt"} {
		if _, ok := m[k]; ok {
			t.Fatalf("response must not contain %q: %s", k, rec.Body.String())
		}
	}
}

func TestOTAMissingDeviceID(t *testing.T) {
	setupPrivacyTest(t)
	rec := postOTA(t, "", privacyTestBody)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", rec.Code)
	}
}
