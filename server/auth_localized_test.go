package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Regression-guard test protecting the Koreanization of auth response wording (backlog F3b). It checks via two paths that work even without a DB:
// ① the requireAuth middleware does not go through pg, so even the actual HTTP
// response body is checked; ② the remaining handler wording is named constants, so the constants themselves are checked.
// The Korean check reuses the assertKoreanError helper built by F3a (contains Hangul, zero Chinese Han characters).

// TestRequireAuthMessagesLocalized confirms with the real HTTP handler that the 401 response body requireAuth returns
// when the token is missing or wrong is Korean and has no Chinese Han characters. This
// middleware does not use the database, so it runs to the end even in environments without a DB.
func TestRequireAuthMessagesLocalized(t *testing.T) {
	s := &Server{jwtKey: []byte(strings.Repeat("k", 32))}
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := s.requireAuth(next)

	cases := []struct {
		name   string
		bearer string
		want   string
	}{
		{"토큰 없음", "", authErrUnauthorized},
		{"토큰 무효", "Bearer not-a-valid-token", authErrTokenInvalid},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/api/tasks", nil)
			if c.bearer != "" {
				r.Header.Set("Authorization", c.bearer)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, r)

			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("상태 코드 = %d, 기대 401", rec.Code)
			}
			body := rec.Body.String()
			if !strings.Contains(body, c.want) {
				t.Errorf("응답 본문에 %q 가 없습니다: %s", c.want, body)
			}
			assertKoreanError(t, c.name, body)
		})
	}
}

// TestAuthErrorConstantsLocalized asserts that all user-facing wording constants used by the auth handlers
// contain Hangul and no Chinese Han characters. It catches regressions in the wording of handlers that cannot be hit
// directly through requireAuth (= those going through pg) without a DB.
func TestAuthErrorConstantsLocalized(t *testing.T) {
	consts := map[string]string{
		"authErrUnauthorized":          authErrUnauthorized,
		"authErrTokenInvalid":          authErrTokenInvalid,
		"authErrPasswordAlreadySet":    authErrPasswordAlreadySet,
		"authErrPasswordEmpty":         authErrPasswordEmpty,
		"authErrNewPasswordEmpty":      authErrNewPasswordEmpty,
		"authErrPasswordHash":          authErrPasswordHash,
		"authErrSaveFailedPrefix":      authErrSaveFailedPrefix,
		"authErrTokenGen":              authErrTokenGen,
		"authErrBadRequest":            authErrBadRequest,
		"authErrPasswordNotInit":       authErrPasswordNotInit,
		"authErrCurrentPasswordWrong":  authErrCurrentPasswordWrong,
		"authErrBadCredential":         authErrBadCredential,
		"authErrPasswordTooShort":      authErrPasswordTooShort,
		"authErrSetupTokenInvalid":     authErrSetupTokenInvalid,
		"authErrTooManyAttempts":       authErrTooManyAttempts,
		"authErrPasswordTooLong":       authErrPasswordTooLong,
		"authErrDataSourceUnavailable": authErrDataSourceUnavailable,
	}
	for name, msg := range consts {
		assertKoreanError(t, name, msg)
	}
}
