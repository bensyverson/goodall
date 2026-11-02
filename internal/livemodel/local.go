package livemodel

import (
	"cmp"
	"net/http"
	"os"
	"time"
)

// LMStudioURLName is the environment variable that points the local tests and
// recordings at another server. It holds no secret, and no key is needed.
const LMStudioURLName = "GOODALL_LMSTUDIO_URL"

// LMStudioURL is LM Studio's documented default.
const LMStudioURL = "http://localhost:1234/v1"

// LMStudioNoModels is the code LM Studio answers with when nothing is loaded,
// which is a machine not set up for a live run rather than a failure of a client.
const LMStudioNoModels = "noModelsLoaded"

// LMStudioBase is the base URL the local tests and recordings use.
func LMStudioBase() string {
	return cmp.Or(os.Getenv(LMStudioURLName), LMStudioURL)
}

// ProbeLMStudio reports whether a server answers at [LMStudioBase], so a caller
// can skip loudly rather than fail on a machine with no server running.
func ProbeLMStudio() error {
	probe := http.Client{Timeout: 2 * time.Second}
	resp, err := probe.Get(LMStudioBase() + "/models")
	if err != nil {
		return err
	}
	return resp.Body.Close()
}
