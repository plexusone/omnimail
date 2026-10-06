package providertest_test

import (
	"testing"

	"github.com/plexusone/omnimail"
	"github.com/plexusone/omnimail/memsender"
	"github.com/plexusone/omnimail/providertest"
)

// memHarness runs the suite against memsender, whose capture is the message
// itself.
type memHarness struct{ s *memsender.Sender }

func (h *memHarness) Sender() omnimail.Sender { return h.s }

func (h *memHarness) Captured(testing.TB) []*omnimail.Message { return h.s.Messages() }

func (h *memHarness) Reset() { h.s.Reset() }

func (h *memHarness) InjectFailure(kind omnimail.Kind) bool {
	h.s.FailNext(omnimail.NewError(kind, memsender.ProviderName, nil))
	return true
}

func TestMemsenderConformance(t *testing.T) {
	providertest.RunAll(t, providertest.Config{
		Harness:                &memHarness{s: memsender.New()},
		Provider:               memsender.ProviderName,
		SupportsTags:           true,
		SupportsIdempotencyKey: true,
		SupportsSMTPUTF8:       true,
	})
}

func TestMemsenderConformanceMinimalCapabilities(t *testing.T) {
	providertest.RunAll(t, providertest.Config{
		Harness:           &memHarness{s: memsender.New()},
		From:              omnimail.Address{Email: "verified@example.com"},
		SkipCustomHeaders: true,
	})
}
