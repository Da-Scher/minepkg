package auth

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/minepkg/minepkg/internals/credentials"
	"github.com/minepkg/minepkg/internals/minecraft"
	"github.com/minepkg/minepkg/internals/minecraft/microsoft"
	"golang.org/x/oauth2"
)

type Microsoft struct {
	*microsoft.MicrosoftClient
	authData *microsoft.Credentials
	Store    *credentials.Store
}

// MicrosoftCredentialStorage is used to trim down the auth data to the minimum required
// otherwise the windows keyring will return an error ("The stub received bad data.")
type MicrosoftCredentialStorage struct {
	MicrosoftAuth oauth2.Token `json:"ms"`
	PlayerName    string       `json:"pn"`
	UUID          string       `json:"id"`
	AccessToken   string       `json:"at"`
	ExpiresAt     time.Time    `json:"exp"`
}

func (m *Microsoft) SetAuthState(authData *MicrosoftCredentialStorage) error {
	log.Printf("Restoring MS auth state")
	m.authData = &microsoft.Credentials{
		ExpiresAt:     authData.ExpiresAt,
		MicrosoftAuth: authData.MicrosoftAuth,
		MinecraftAuth: &microsoft.XboxLoginResponse{
			AccessToken: authData.AccessToken,
		},
		MinecraftProfile: &microsoft.GetProfileResponse{
			ID:   authData.UUID,
			Name: authData.PlayerName,
		},
	}
	m.SetOauthToken(&authData.MicrosoftAuth)
	return nil
}

func (m *Microsoft) Prompt() error {
	ctx := context.Background()
	if err := m.Oauth(context.Background()); err != nil {
		return err
	}

	creds, err := m.GetMinecraftCredentials(ctx)
	if err != nil {
		return err
	}
	m.authData = creds
	if err := m.persist(); err != nil {
		return err
	}
	return nil
}

func (m *Microsoft) LaunchAuthData() (minecraft.LaunchAuthData, error) {
	// not auth data or it is expired
	if m.authData == nil || m.authData.IsExpired() {
		log.Println("Refreshing MS auth data")
		return m.refreshAuthData()
	}
	// we have valid and unexpired auth data
	log.Println("Using Cached MS auth data")
	return m.authData, nil
}

func (m *Microsoft) refreshAuthData() (*microsoft.Credentials, error) {
	creds, err := m.GetMinecraftCredentials(context.Background())
	if err != nil {
		return nil, err
	}
	m.authData = creds
	if err := m.persist(); err != nil {
		return nil, err
	}
	return creds, err
}

func (m *Microsoft) persist() error {
	log.Println("Persisting MS auth data")
	trimmedData := &MicrosoftCredentialStorage{
		ExpiresAt:     m.authData.ExpiresAt,
		MicrosoftAuth: m.authData.MicrosoftAuth,
		AccessToken:   m.authData.MinecraftAuth.AccessToken,
		UUID:          m.authData.MinecraftProfile.ID,
		PlayerName:    m.authData.MinecraftProfile.Name,
	}
	data, err := json.Marshal(trimmedData)
	if err != nil {
		return err
	}
	return m.Store.Set(&PersistentCredentials{
		Provider: "microsoft",
		Data:     data,
	})
}
