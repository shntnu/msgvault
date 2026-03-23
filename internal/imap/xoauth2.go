package imap

import (
	"fmt"

	"github.com/emersion/go-sasl"
)

// xoauth2Client implements sasl.Client for the XOAUTH2 mechanism
// used by Microsoft Exchange Online and Gmail IMAP.
//
// The initial response format is:
//
//	"user=" + username + "\x01" + "auth=Bearer " + token + "\x01\x01"
//
// See https://developers.google.com/gmail/imap/xoauth2-protocol
type xoauth2Client struct {
	username string
	token    string
}

// NewXOAuth2Client creates a SASL client for XOAUTH2 authentication.
func NewXOAuth2Client(username, token string) sasl.Client {
	return &xoauth2Client{username: username, token: token}
}

func (c *xoauth2Client) Start() (mech string, ir []byte, err error) {
	resp := "user=" + c.username + "\x01auth=Bearer " + c.token + "\x01\x01"
	return "XOAUTH2", []byte(resp), nil
}

func (c *xoauth2Client) Next(challenge []byte) ([]byte, error) {
	return nil, fmt.Errorf("XOAUTH2: unexpected server challenge")
}
