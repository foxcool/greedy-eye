package portfolio

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	apiv1 "github.com/foxcool/greedy-eye/api/v1"
	"github.com/foxcool/greedy-eye/internal/provider/catalog"
)

// errNoProviderCatalog is the answer of a build that composes no adapters. Said
// out loud rather than answered with an empty list: "there are no providers" is
// a different statement from "this deployment cannot tell you", and a form that
// confuses them offers a person nothing and explains nothing.
var errNoProviderCatalog = errors.New("this deployment serves no provider catalogue")

// ProviderCatalog lists the external providers this build can talk to.
//
// An interface rather than the registry itself: the service describes
// providers, it does not construct them, and a deployment that serves
// portfolios without composing adapters (microservice mode) still answers the
// question by being handed a catalogue.
type ProviderCatalog interface {
	Descriptors() []catalog.Descriptor
}

// WithProviderCatalog returns a new Handler that can describe the providers an
// account may be created against. Without it ListProviders reports that this
// build has nothing to describe, rather than an empty catalogue — an empty list
// reads as "no providers exist", which would be a lie about the system.
func (h *Handler) WithProviderCatalog(pc ProviderCatalog) *Handler {
	copied := h.clone()
	copied.providers = pc
	return copied
}

// ListProviders describes what each provider needs before an account can use
// it: the slug to name, whether a key and secret are wanted, which chains it
// reads, which plans it is metered under, and any extra field it cannot work
// without.
//
// No credential is read and none is returned. The catalogue is a statement
// about the build, identical for every caller, which is why it needs no
// ownership check and no pagination.
func (h *Handler) ListProviders(
	ctx context.Context,
	_ *connect.Request[apiv1.ListProvidersRequest],
) (*connect.Response[apiv1.ListProvidersResponse], error) {
	if h.providers == nil {
		return nil, connect.NewError(connect.CodeUnimplemented,
			errNoProviderCatalog)
	}

	descriptors := h.providers.Descriptors()
	out := make([]*apiv1.Provider, 0, len(descriptors))
	for _, d := range descriptors {
		out = append(out, providerToProto(d))
	}
	return connect.NewResponse(&apiv1.ListProvidersResponse{Providers: out}), nil
}

func providerToProto(d catalog.Descriptor) *apiv1.Provider {
	p := &apiv1.Provider{
		Slug:           d.Slug,
		Title:          d.Title,
		Kinds:          make([]apiv1.ProviderKind, 0, len(d.Kinds)),
		Capabilities:   d.Capabilities,
		NeedsApiKey:    d.NeedsAPIKey,
		NeedsApiSecret: d.NeedsAPISecret,
		Keyless:        d.Keyless,
		Chains:         d.Chains,
		Fields:         make([]*apiv1.ProviderField, 0, len(d.Fields)),
		Tiers:          make([]*apiv1.ProviderTier, 0, len(d.Tiers)),
	}
	for _, k := range d.Kinds {
		p.Kinds = append(p.Kinds, kindToProto(k))
	}
	for _, f := range d.Fields {
		p.Fields = append(p.Fields, &apiv1.ProviderField{
			Key:       f.Key,
			Title:     f.Title,
			Help:      f.Help,
			Required:  f.Required,
			Multiline: f.Multiline,
		})
	}
	for _, tier := range d.Tiers {
		p.Tiers = append(p.Tiers, &apiv1.ProviderTier{
			Name:        tier.Name,
			Rps:         tier.Limit.RPS,
			Burst:       int32(tier.Limit.Burst), //nolint:gosec // plan burst is a small positive count
			Quota:       int32(tier.Limit.Quota), //nolint:gosec // plan volume fits an int32 with room to spare
			QuotaPeriod: string(tier.Limit.Period),
		})
	}
	return p
}

func kindToProto(k catalog.Kind) apiv1.ProviderKind {
	switch k {
	case catalog.KindPrice:
		return apiv1.ProviderKind_PROVIDER_KIND_PRICE
	case catalog.KindWallet:
		return apiv1.ProviderKind_PROVIDER_KIND_WALLET
	case catalog.KindExchange:
		return apiv1.ProviderKind_PROVIDER_KIND_EXCHANGE
	default:
		return apiv1.ProviderKind_PROVIDER_KIND_UNSPECIFIED
	}
}
