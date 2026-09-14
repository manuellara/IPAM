package middleware

import "net/http"

type Middleware func(http.Handler) http.Handler

// MiddlewareStack creates a stack of middlewares.
// The middlewares are applied in the order they are provided,
// with the first middleware being the outermost and the last middleware being the innermost.
func MiddlewareStack(middlewares ...Middleware) Middleware {
	return func(next http.Handler) http.Handler {

		for i := len(middlewares) - 1; i >= 0; i-- {
			x := middlewares[i]

			next = x(next)
		}

		return next
	}
}
