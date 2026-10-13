package main

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"strings"
	"testing"
	"time"
)

// makePNG returns PNG-encoded bytes of a small solid image.
func makePNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			img.Set(x, y, color.RGBA{R: 200, G: 100, B: 50, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("failed to encode test PNG: %v", err)
	}
	return buf.Bytes()
}

// makeJPEG returns JPEG-encoded bytes of a small solid image.
func makeJPEG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			img.Set(x, y, color.RGBA{R: 50, G: 100, B: 200, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatalf("failed to encode test JPEG: %v", err)
	}
	return buf.Bytes()
}

// 1x1 lossy WebP (well-known minimal fixture).
const testWebPBase64 = "UklGRiIAAABXRUJQVlA4IBYAAAAwAQCdASoBAAEADsD+JaQAA3AAAAAA"

func b64(t *testing.T, raw []byte) []byte {
	t.Helper()
	return []byte(base64.StdEncoding.EncodeToString(raw))
}

// TestProcessAndConvertFiles_ContentSniffing verifies the transcoder selects
// the decoder based on the actual bytes rather than the declared Content-Type.
func TestProcessAndConvertFiles_ContentSniffing(t *testing.T) {
	webpRaw, err := base64.StdEncoding.DecodeString(testWebPBase64)
	if err != nil {
		t.Fatalf("bad webp fixture: %v", err)
	}

	tests := []struct {
		name            string
		declaredType    string
		content         []byte // raw (pre-base64) bytes
		wantErr         string // substring; empty = expect success
		wantContentType string // expected output ContentType on success
	}{
		{
			name:            "jpeg bytes labeled png",
			declaredType:    "image/png",
			content:         makeJPEG(t),
			wantContentType: "image/jpeg",
		},
		{
			name:            "png bytes labeled jpeg",
			declaredType:    "image/jpeg",
			content:         makePNG(t),
			wantContentType: "image/png",
		},
		{
			name:            "webp bytes labeled png",
			declaredType:    "image/png",
			content:         webpRaw,
			wantContentType: "image/png", // converted to PNG
		},
		{
			name:            "correctly labeled png (control)",
			declaredType:    "image/png",
			content:         makePNG(t),
			wantContentType: "image/png",
		},
		{
			name:         "html error page labeled png",
			declaredType: "image/png",
			content:      []byte("<html><head><title>401 Unauthorized</title></head><body>login</body></html>"),
			wantErr:      "error page",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			lm := NewLogManager(nil, false)
			msg := &MM4Message{
				From:          "+15550001",
				To:            "+15550002",
				TransactionID: "test-" + tc.name,
				MessageID:     "test-" + tc.name,
				Files: []MsgFile{{
					Filename:    "media" + strings.ReplaceAll(tc.declaredType, "/", "."),
					ContentType: tc.declaredType,
					Content:     b64(t, tc.content),
				}},
			}

			files, _, err := msg.processAndConvertFiles(lm)

			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("expected error containing %q, got: %v", tc.wantErr, err)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(files) != 1 {
				t.Fatalf("expected 1 output file, got %d", len(files))
			}
			if files[0].ContentType != tc.wantContentType {
				t.Fatalf("expected output ContentType %q, got %q", tc.wantContentType, files[0].ContentType)
			}
		})
	}
}

// TestTranscodeMediaFailure_DoesNotMutateClientPassword is a regression test
// for the auth-cache poisoning bug: the transcode error path used to overwrite
// the shared in-memory client Password with "***", breaking auth in both
// directions until the next client reload.
func TestTranscodeMediaFailure_DoesNotMutateClientPassword(t *testing.T) {
	lm := NewLogManager(nil, false)
	router := &Router{
		CarrierMsgChan: make(chan MsgQueueItem, 1),
	}
	gateway := &Gateway{
		LogManager: lm,
		Router:     router,
	}
	router.gateway = gateway
	srv := &MM4Server{
		gateway:            gateway,
		MediaTranscodeChan: make(chan *MM4Message, 1),
	}

	client := &Client{Username: "tops_bicom1", Password: "supersecret"}

	// A message whose media will fail transcoding (HTML body labeled PNG).
	mm4Message := &MM4Message{
		From:          "+15550001",
		To:            "+15550002",
		Client:        client,
		TransactionID: "test-poison-regression",
		MessageID:     "test-poison-regression",
		Files: []MsgFile{{
			Filename:    "photo.png",
			ContentType: "image/png",
			Content:     b64(t, []byte("<html><body>not an image</body></html>")),
		}},
	}

	go srv.transcodeMedia()
	srv.MediaTranscodeChan <- mm4Message

	// The error path sends a bounce message to the carrier channel.
	select {
	case bounce := <-router.CarrierMsgChan:
		if bounce.LogID != "test-poison-regression" {
			t.Errorf("unexpected bounce LogID: %s", bounce.LogID)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for bounce message from transcode error path")
	}

	if client.Password != "supersecret" {
		t.Fatalf("client password was mutated by transcode error path: got %q, want %q",
			client.Password, "supersecret")
	}
}
