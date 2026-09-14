package middleware

import "net/http"

func CsrfMiddleware(next http.Handler) http.Handler {
	// cross-origin protection middleware only checks unsafe HTTP methods (POST, PATCH, DELETE) and same-origin policy for requests with an Origin header
	// older browsers may not send Origin header for same-origin requests, so cross-origin protection middleware will allow those requests by default (which is considered a security gap)
	// if you have a separate frontend application that needs to make cross-origin requests to this API, you can add the frontend origin to the trusted origins list in the cross-origin protection middleware configuration
	// use cross-origin protection middleware for all routes that require authentication to prevent CSRF attacks
	// use cross-origin protection middleware in combination with SameSite cookies for best protection against CSRF attacks
	// if IdP uses POST requests for authentication, you may need to add the IdP origin to the trusted origins list in the cross-origin protection middleware configuration to prevent authentication issues

	// create cross-origin protection middleware
	cop := http.NewCrossOriginProtection()

	// (optional) add trusted origins for cross-origin protection
	// cop.AddTrustedOrigin("https://frontend.example.com")

	// (optional) safely bypasses CSRF middleware evaluation for the OIDC landing path
	// cop.AddInsecureBypassPattern("/callback")

	return cop.Handler(next)
}