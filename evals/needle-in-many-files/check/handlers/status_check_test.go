package handlers

import (
	"net/http"
	"testing"
)

func TestStatusCheck(t *testing.T) {
	for i, c := range []struct{ got, want int }{
		{Continue(), http.StatusContinue},
		{OK(), http.StatusOK},
		{Created(), http.StatusCreated},
		{Accepted(), http.StatusAccepted},
		{NoContent(), http.StatusNoContent},
		{MovedPermanently(), http.StatusMovedPermanently},
		{Found(), http.StatusFound},
		{NotModified(), http.StatusNotModified},
		{BadRequest(), http.StatusBadRequest},
		{Unauthorized(), http.StatusUnauthorized},
		{PaymentRequired(), http.StatusPaymentRequired},
		{Forbidden(), http.StatusForbidden},
		{NotFound(), http.StatusNotFound},
		{MethodNotAllowed(), http.StatusMethodNotAllowed},
		{NotAcceptable(), http.StatusNotAcceptable},
		{RequestTimeout(), http.StatusRequestTimeout},
		{Conflict(), http.StatusConflict},
		{Gone(), http.StatusGone},
		{LengthRequired(), http.StatusLengthRequired},
		{PreconditionFailed(), http.StatusPreconditionFailed},
		{RequestEntityTooLarge(), http.StatusRequestEntityTooLarge},
		{UnsupportedMediaType(), http.StatusUnsupportedMediaType},
		{Teapot(), http.StatusTeapot},
		{UnprocessableEntity(), http.StatusUnprocessableEntity},
		{Locked(), http.StatusLocked},
		{TooEarly(), http.StatusTooEarly},
		{UpgradeRequired(), http.StatusUpgradeRequired},
		{TooManyRequests(), http.StatusTooManyRequests},
		{InternalServerError(), http.StatusInternalServerError},
		{NotImplemented(), http.StatusNotImplemented},
		{BadGateway(), http.StatusBadGateway},
		{ServiceUnavailable(), http.StatusServiceUnavailable},
		{GatewayTimeout(), http.StatusGatewayTimeout},
	} {
		if c.got != c.want {
			t.Errorf("case %d: %d, want %d", i, c.got, c.want)
		}
	}
}
