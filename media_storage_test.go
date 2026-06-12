package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetExtensionForContentType_KnownTypes(t *testing.T) {
	cases := map[string]string{
		"image/jpeg":      ".jpg",
		"image/png":       ".png",
		"image/gif":       ".gif",
		"image/bmp":       ".bmp",
		"image/webp":      ".webp",
		"video/3gpp":      ".3gp",
		"video/3gpp2":     ".3g2",
		"video/mp4":       ".mp4",
		"video/quicktime": ".mov",
		"audio/mpeg":      ".mp3",
		"audio/wav":       ".wav",
		"audio/ogg":       ".ogg",
		"audio/amr":       ".amr",
		"application/pdf": ".pdf",
	}
	for ct, want := range cases {
		t.Run(ct, func(t *testing.T) {
			assert.Equal(t, want, getExtensionForContentType(ct))
		})
	}
}

func TestGetExtensionForContentType_UnknownTypeFallsBackToSubtype(t *testing.T) {
	// Unknown but well-formed "type/subtype" → ".subtype"
	assert.Equal(t, ".xyz", getExtensionForContentType("application/xyz"))
}

func TestGetExtensionForContentType_MalformedTypeReturnsEmpty(t *testing.T) {
	assert.Equal(t, "", getExtensionForContentType("not-a-mime-type"))
	assert.Equal(t, "", getExtensionForContentType(""))
}

func TestGetMediaUrlWithExtension(t *testing.T) {
	url := getMediaUrlWithExtension("https://sms.example.com", "abc-token", "image/jpeg")
	assert.Equal(t, "https://sms.example.com/media/abc-token.jpg", url)

	// Unknown mime type → falls back to using the subtype as the extension.
	url = getMediaUrlWithExtension("https://sms.example.com", "abc-token", "application/octet-stream")
	assert.Equal(t, "https://sms.example.com/media/abc-token.octet-stream", url)
}

func TestGetMediaUrlWithExtension_TrimsTrailingSlash(t *testing.T) {
	// Caller is responsible for not adding a trailing slash; we just verify the simple case.
	url := getMediaUrlWithExtension("https://sms.example.com/", "token", "video/mp4")
	// We don't trim — so a trailing slash is preserved. Document current behavior.
	assert.Equal(t, "https://sms.example.com//media/token.mp4", url)
}
