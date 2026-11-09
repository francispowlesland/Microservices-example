package service

import "log/slog"

// Mailer delivers one-time tokens to users. Real delivery is out of scope.
type Mailer interface {
	SendVerification(email, token string) error
	SendPasswordReset(email, token string) error
}

// LogMailer writes the token to the log so a developer can complete flows
// locally. DEV ONLY: it deliberately logs secrets. main refuses to start with
// it when APP_ENV=production.
type LogMailer struct{ Logger *slog.Logger }

func (m LogMailer) logger() *slog.Logger {
	if m.Logger != nil {
		return m.Logger
	}
	return slog.Default()
}

func (m LogMailer) SendVerification(email, token string) error {
	m.logger().Info("DEV MAILER: verification email", slog.String("to", email), slog.String("verification_token", token))
	return nil
}

func (m LogMailer) SendPasswordReset(email, token string) error {
	m.logger().Info("DEV MAILER: password reset email", slog.String("to", email), slog.String("reset_token", token))
	return nil
}
