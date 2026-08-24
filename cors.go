package main

import (
	"net/http"
	"os"
	"strings"

	"github.com/kataras/iris/v12"
)

// CORSMiddleware lets browser-based clients (the admin control panel SPA) call
// the API from a different origin. Allowed origins are configured via the
// CORS_ALLOWED_ORIGINS env var (comma-separated). Defaults to "*" because API
// access is already gated by the API_KEY — CORS only governs which browser
// origins may *attempt* requests, and any origin still needs the key.
// Set CORS_ALLOWED_ORIGINS to a comma-separated allowlist (e.g.
// "https://admin.example.com") or to "off" to disable CORS headers entirely.
func CORSMiddleware(ctx iris.Context) {
	allowed := os.Getenv("CORS_ALLOWED_ORIGINS")
	if allowed == "off" {
		ctx.Next()
		return
	}

	origin := ctx.GetHeader("Origin")
	if origin != "" {
		if allowed == "" || allowed == "*" {
			ctx.Header("Access-Control-Allow-Origin", "*")
		} else {
			for _, o := range strings.Split(allowed, ",") {
				if strings.TrimSpace(o) == origin {
					ctx.Header("Access-Control-Allow-Origin", origin)
					ctx.Header("Vary", "Origin")
					break
				}
			}
		}
		ctx.Header("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		ctx.Header("Access-Control-Allow-Headers", "Authorization, Content-Type")
		ctx.Header("Access-Control-Max-Age", "86400")
	}

	// Answer preflight requests directly — they carry no credentials and must
	// not hit the auth middleware.
	if ctx.Method() == http.MethodOptions {
		ctx.StatusCode(http.StatusNoContent)
		return
	}

	ctx.Next()
}
