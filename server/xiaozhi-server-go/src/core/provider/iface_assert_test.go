package provider

import (
	"testing"

	"xiaozhi-server-go/src/core/providers"
	asropenai "xiaozhi-server-go/src/core/providers/asr/openai"
	ttsopenai "xiaozhi-server-go/src/core/providers/tts/openai"
)

// The manager type-asserts factory output to these interfaces at connect time;
// a missing method is a runtime panic, so pin it at compile time.
var (
	_ providers.ASRProvider = (*asropenai.Provider)(nil)
	_ providers.TTSProvider = (*ttsopenai.Provider)(nil)
)

func TestOpenAIProvidersSatisfyManagerInterfaces(t *testing.T) {}
