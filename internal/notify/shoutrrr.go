package notify

import (
	"context"
	"errors"
	"net/http"

	"github.com/nicholas-fedor/shoutrrr/pkg/router"
	"github.com/nicholas-fedor/shoutrrr/pkg/types"

	"github.com/NexusBetweenVoids/gunkly/internal/egress"
)

// ShoutrrrSender delivers through Shoutrrr with Gunkly's egress-guarded HTTP
// client and dial function injected into every service (SetHTTPClient for
// HTTP services, SetDialContext for SMTP, MQTT and XMPP). The pinned Shoutrrr
// version is covered by TestSendersBlocked: an upgrade that bypasses the
// guard fails CI.
type ShoutrrrSender struct {
	client *http.Client
	dial   types.DialContextFunc
}

// NewShoutrrrSender returns a Sender whose every connection goes through g.
func NewShoutrrrSender(g egress.Guard) *ShoutrrrSender {
	return &ShoutrrrSender{client: egress.NewClient(g), dial: g.DialContext}
}

// errInit hides Shoutrrr's initialisation error, which may quote the URL.
var errInit = errors.New("notify: service URL rejected by the sender")

// Send implements Sender. Errors may contain parts of rawURL: pass them
// through SafeError before storing or logging.
func (s *ShoutrrrSender) Send(ctx context.Context, rawURL string, m Message) error {
	r, err := router.NewWithOptions(nil, types.SenderOptions{HTTPClient: s.client, DialContext: s.dial}, rawURL)
	if err != nil {
		return errInit
	}
	defer func() { _ = r.Close() }()
	params := types.Params{types.TitleKey: m.Title}
	return errors.Join(r.SendContext(ctx, m.Body, &params)...)
}
