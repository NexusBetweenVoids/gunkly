package notify

import (
	"context"

	"github.com/NexusBetweenVoids/gunkly/internal/egress"
)

// SwitchSender is a Sender whose egress guard follows an instance setting: it
// asks guarded on every send, so an admin change applies at once without
// rebuilding the sender. Both transports always keep timeouts, the body cap
// and the redirect policy; only the address check differs.
type SwitchSender struct {
	guarded, open *ShoutrrrSender
	guard         func(context.Context) bool
}

// NewSwitchSender returns a Sender that uses the guarded transport when guard
// returns true. If guard cannot decide it must return true (fail closed).
func NewSwitchSender(guard func(context.Context) bool) *SwitchSender {
	return &SwitchSender{
		guarded: NewShoutrrrSender(egress.Guard{Enabled: true}),
		open:    NewShoutrrrSender(egress.Guard{Enabled: false}),
		guard:   guard,
	}
}

// Send implements Sender.
func (s *SwitchSender) Send(ctx context.Context, rawURL string, m Message) error {
	if s.guard(ctx) {
		return s.guarded.Send(ctx, rawURL, m)
	}
	return s.open.Send(ctx, rawURL, m)
}
