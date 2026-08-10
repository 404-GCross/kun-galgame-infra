package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"api/pkg/errors"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
)

type okPayload struct {
	X int `json:"x"`
}

type okOutput struct {
	Body Envelope[okPayload]
}

func TestEnvelopeSuccess(t *testing.T) {
	InstallErrorEnvelope()
	_, api := humatest.New(t)
	huma.Register(api, huma.Operation{OperationID: "test-ok", Method: http.MethodGet, Path: "/ok"},
		func(_ context.Context, _ *struct{}) (*okOutput, error) {
			return &okOutput{Body: okEnvelope(okPayload{X: 7})}, nil
		})

	resp := api.Get("/ok")
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", resp.Code, resp.Body.String())
	}
	var got Envelope[okPayload]
	if err := json.Unmarshal(resp.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v; body=%s", err, resp.Body.String())
	}
	if got.Code != 0 || got.Message != msgOK || got.Data.X != 7 {
		t.Fatalf("envelope = %+v, want {code:0, message:成功, data:{x:7}}", got)
	}
}

func TestEnvelopeError(t *testing.T) {
	InstallErrorEnvelope()
	_, api := humatest.New(t)
	huma.Register(api, huma.Operation{OperationID: "test-err", Method: http.MethodGet, Path: "/err"},
		func(_ context.Context, _ *struct{}) (*struct{}, error) {
			return nil, apiErr(http.StatusNotFound, errors.ErrArtifactNotFound)
		})

	resp := api.Get("/err")
	if resp.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", resp.Code, resp.Body.String())
	}
	if ct := resp.Header().Get("Content-Type"); !strings.Contains(ct, "json") || strings.Contains(ct, "problem") {
		t.Fatalf("content-type = %q, want a non-problem json type", ct)
	}
	var got Envelope[json.RawMessage]
	if err := json.Unmarshal(resp.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v; body=%s", err, resp.Body.String())
	}
	if got.Code != errors.ErrArtifactNotFound || got.Message != errors.GetMessage(errors.ErrArtifactNotFound) {
		t.Fatalf("error envelope = %+v, want code=%d msg=%q", got, errors.ErrArtifactNotFound, errors.GetMessage(errors.ErrArtifactNotFound))
	}
}

func TestEnvelopeInternalError(t *testing.T) {
	InstallErrorEnvelope()
	_, api := humatest.New(t)
	huma.Register(api, huma.Operation{OperationID: "test-internal", Method: http.MethodGet, Path: "/boom"},
		func(_ context.Context, _ *struct{}) (*struct{}, error) {
			return nil, huma.Error500InternalServerError("kaboom")
		})

	resp := api.Get("/boom")
	if resp.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body=%s", resp.Code, resp.Body.String())
	}
	var got Envelope[json.RawMessage]
	if err := json.Unmarshal(resp.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v; body=%s", err, resp.Body.String())
	}
	if got.Code != errors.ErrArtifactStoreFailed {
		t.Fatalf("internal error envelope = %+v, want code=%d", got, errors.ErrArtifactStoreFailed)
	}
}
