// Copyright 2026 Teradata
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package chatgpt

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

var (
	// ErrUsageLimit is matched (errors.Is) by a *PlanError for a ChatGPT
	// user who reached the usage limit they share with apps (the plan's
	// limit, or the per-app weekly cap they set in ChatGPT).
	ErrUsageLimit = errors.New("chatgpt: the plan's usage limit for this app is reached")
	// ErrNotEligible is matched by a *PlanError for a user whose plan or
	// region cannot share plan usage with apps.
	ErrNotEligible = errors.New("chatgpt: this ChatGPT account cannot use its plan in apps")
	// ErrSignInAgain is returned when the saved sign-in can no longer be
	// used (the refresh token expired, was reused or revoked, or the grant
	// lacks plan usage): the user has to sign in again.
	ErrSignInAgain = errors.New("chatgpt: sign in with ChatGPT again")
	// ErrNotSignedIn is returned when there are no saved credentials.
	ErrNotSignedIn = errors.New("chatgpt: not signed in with ChatGPT")
	// ErrOtherUser is returned for a request made for a different user than
	// the one who signed in: a ChatGPT plan answers only its own user.
	ErrOtherUser = errors.New("chatgpt: this ChatGPT plan answers only the person who signed in")
)

// PlanError is an error from plan usage: an HTTP error before the stream
// opened, or a response.failed / error event after it.
type PlanError struct {
	// StatusCode is the HTTP status, or 0 for an error inside the stream.
	StatusCode int
	// Code is the API's error code, e.g.
	// "subscription_sharing_usage_limit_exceeded".
	Code    string
	Message string
	// Param names the request field an unsupported-capability error is
	// about.
	Param string
}

func newPlanError(status int, code, message, param string) *PlanError {
	return &PlanError{StatusCode: status, Code: code, Message: message, Param: param}
}

func (e *PlanError) Error() string {
	msg := e.Message
	if msg == "" {
		msg = http.StatusText(e.StatusCode)
	}
	s := "chatgpt: " + msg
	if e.Code != "" {
		s += " (" + e.Code + ")"
	}
	if e.Param != "" {
		s += " [param " + e.Param + "]"
	}
	if e.StatusCode != 0 {
		s = fmt.Sprintf("%s: HTTP %d", s, e.StatusCode)
	}
	return s
}

// Is matches ErrUsageLimit, ErrNotEligible and ErrSignInAgain by error
// code (and ErrSignInAgain by HTTP 401).
func (e *PlanError) Is(target error) bool {
	switch target {
	case ErrUsageLimit:
		return e.Code == "subscription_sharing_usage_limit_exceeded"
	case ErrNotEligible:
		return e.Code == "subscription_sharing_user_not_eligible"
	case ErrSignInAgain:
		return e.Code == "subscription_sharing_invalid_user" || e.Code == "chatpass_v2_scope_not_authorized" ||
			e.StatusCode == http.StatusUnauthorized
	}
	return false
}

// Retryable reports whether the same request may succeed later without the
// user doing anything: the service was briefly unavailable.
func (e *PlanError) Retryable() bool {
	return e.Code == "subscription_sharing_usage_unavailable" || e.StatusCode == http.StatusServiceUnavailable ||
		e.StatusCode == http.StatusBadGateway || e.StatusCode == http.StatusGatewayTimeout
}

// httpError builds a *PlanError from an error response body: the Responses
// API's {"error":{...}} or the gateway's {"detail":...}.
func httpError(status int, body []byte) *PlanError {
	var wrapped struct {
		Error  *apiError       `json:"error"`
		Detail json.RawMessage `json:"detail"`
	}
	if json.Unmarshal(body, &wrapped) == nil {
		if wrapped.Error != nil {
			return newPlanError(status, wrapped.Error.Code, wrapped.Error.Message, wrapped.Error.Param)
		}
		if len(wrapped.Detail) > 0 {
			var detail string
			if json.Unmarshal(wrapped.Detail, &detail) != nil {
				detail = string(wrapped.Detail)
			}
			return newPlanError(status, "", detail, "")
		}
	}
	msg := string(body)
	if len(msg) > 500 {
		msg = msg[:500]
	}
	return newPlanError(status, "", msg, "")
}
