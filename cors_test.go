package main

import (
	"net/http"
	"testing"

	"github.com/kataras/iris/v12"
	"github.com/kataras/iris/v12/httptest"
)

func newCORSApp(t *testing.T) *iris.Application {
	t.Helper()
	app := iris.New()
	app.UseRouter(CORSMiddleware)
	app.Get("/stats", func(ctx iris.Context) {
		ctx.JSON(iris.Map{"ok": true})
	})
	if err := app.Build(); err != nil {
		t.Fatalf("failed to build app: %v", err)
	}
	return app
}

func TestCORS_PreflightShortCircuits(t *testing.T) {
	t.Setenv("CORS_ALLOWED_ORIGINS", "")
	app := newCORSApp(t)

	resp := httptest.New(t, app).
		OPTIONS("/stats").
		WithHeader("Origin", "http://localhost:5173").
		WithHeader("Access-Control-Request-Method", "GET").
		Expect()
	resp.Status(http.StatusNoContent)
	resp.Header("Access-Control-Allow-Origin").IsEqual("*")
	resp.Header("Access-Control-Allow-Methods").IsEqual("GET, POST, PUT, PATCH, DELETE, OPTIONS")
	resp.Header("Access-Control-Allow-Headers").IsEqual("Authorization, Content-Type")
}

func TestCORS_DefaultWildcardOnRequests(t *testing.T) {
	t.Setenv("CORS_ALLOWED_ORIGINS", "")
	app := newCORSApp(t)

	httptest.New(t, app).
		GET("/stats").
		WithHeader("Origin", "http://localhost:5173").
		Expect().
		Status(http.StatusOK).
		Header("Access-Control-Allow-Origin").IsEqual("*")
}

func TestCORS_AllowlistMatchesOrigin(t *testing.T) {
	t.Setenv("CORS_ALLOWED_ORIGINS", "https://admin.example.com, https://admin2.example.com")
	app := newCORSApp(t)

	httptest.New(t, app).
		GET("/stats").
		WithHeader("Origin", "https://admin2.example.com").
		Expect().
		Status(http.StatusOK).
		Header("Access-Control-Allow-Origin").IsEqual("https://admin2.example.com")
}

func TestCORS_AllowlistRejectsUnknownOrigin(t *testing.T) {
	t.Setenv("CORS_ALLOWED_ORIGINS", "https://admin.example.com")
	app := newCORSApp(t)

	httptest.New(t, app).
		GET("/stats").
		WithHeader("Origin", "https://evil.example.com").
		Expect().
		Status(http.StatusOK).
		Header("Access-Control-Allow-Origin").IsEqual("")
}

func TestCORS_OffDisablesHeaders(t *testing.T) {
	t.Setenv("CORS_ALLOWED_ORIGINS", "off")
	app := newCORSApp(t)

	httptest.New(t, app).
		GET("/stats").
		WithHeader("Origin", "http://localhost:5173").
		Expect().
		Status(http.StatusOK).
		Header("Access-Control-Allow-Origin").IsEqual("")
}

func TestCORS_NoOriginHeaderUntouched(t *testing.T) {
	t.Setenv("CORS_ALLOWED_ORIGINS", "")
	app := newCORSApp(t)

	// Non-browser clients (no Origin header) get no CORS headers at all.
	httptest.New(t, app).
		GET("/stats").
		Expect().
		Status(http.StatusOK).
		Header("Access-Control-Allow-Origin").IsEqual("")
}
