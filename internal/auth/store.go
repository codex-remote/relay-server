package auth

import (
	"context"
	"time"
)

type Store interface {
	CreatePairingGrant(context.Context, PairingGrant) error
	ExchangePairingGrant(context.Context, []byte, time.Time, CredentialMaterial) (Client, error)
	AuthenticateAccess(context.Context, string, []byte, time.Time) (Principal, error)
	RotateRefresh(context.Context, string, []byte, time.Time, RotatedMaterial) (Client, time.Time, error)
	RevokeSession(context.Context, string, []byte, time.Time, string) error
	ListClients(context.Context) ([]Client, error)
	RevokeClient(context.Context, string, time.Time, string) error
	Health(context.Context) error
	Close()
}
