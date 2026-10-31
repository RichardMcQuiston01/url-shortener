package admin

import (
	"crypto/sha256"
	"crypto/subtle"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

func (handler *Handler) showLogin(responseWriter http.ResponseWriter, request *http.Request, current *session) {
	if current != nil && current.Authenticated {
		http.Redirect(responseWriter, request, "/admin", http.StatusSeeOther)
		return
	}
	if current == nil {
		fresh, err := newSession(handler.now(), false)
		if err == nil {
			err = handler.startSession(responseWriter, fresh)
		}
		if err != nil {
			handler.fail(responseWriter, "start login session", err)
			return
		}
		current = &fresh
	}
	handler.render(responseWriter, http.StatusOK, "login", current, "Sign in", loginContent{})
}

func (handler *Handler) submitLogin(responseWriter http.ResponseWriter, request *http.Request, current *session) {
	clientAddress := clientIP(request, handler.trustProxy)

	if blocked, retryAfter := handler.limiter.blocked(clientAddress); blocked {
		handler.logger.Warn("admin login blocked: too many failures", "client_ip", clientAddress)
		responseWriter.Header().Set("Retry-After", strconv.Itoa(int(retryAfter.Seconds())))
		minutes := int((retryAfter + time.Minute - 1) / time.Minute) // round up
		handler.renderError(responseWriter, current, http.StatusTooManyRequests, "Too many attempts",
			fmt.Sprintf("Too many failed sign-in attempts. Try again in %d minute(s).", minutes),
			"/admin/login", "Back to the sign-in page")
		return
	}

	submittedHash := sha256.Sum256([]byte(request.PostForm.Get("password")))
	if subtle.ConstantTimeCompare(submittedHash[:], handler.passwordHash[:]) != 1 {
		failures := handler.limiter.recordFailure(clientAddress)
		// The submitted password is deliberately never logged.
		handler.logger.Warn("admin login failed", "client_ip", clientAddress, "failures_in_window", failures)
		handler.render(responseWriter, http.StatusUnauthorized, "login", current, "Sign in",
			loginContent{Error: "Incorrect password."})
		return
	}

	handler.limiter.reset(clientAddress)
	signedIn, err := newSession(handler.now(), true)
	if err == nil {
		err = handler.startSession(responseWriter, signedIn)
	}
	if err != nil {
		handler.fail(responseWriter, "start admin session", err)
		return
	}
	http.Redirect(responseWriter, request, "/admin", http.StatusSeeOther)
}

func (handler *Handler) logout(responseWriter http.ResponseWriter, request *http.Request, _ *session) {
	handler.endSession(responseWriter)
	http.Redirect(responseWriter, request, "/admin/login", http.StatusSeeOther)
}

// fail logs the detail and shows a generic error page.
func (handler *Handler) fail(responseWriter http.ResponseWriter, action string, err error) {
	handler.logger.Error("admin request failed", "action", action, "error", err)
	handler.renderError(responseWriter, nil, http.StatusInternalServerError, "Something went wrong",
		"The request could not be completed. Details were written to the server log.", "/admin", "Back to the dashboard")
}
