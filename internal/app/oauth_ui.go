package app

import (
	"context"
	"time"

	"github.com/baba983/vscroxy/internal/request"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// oauthState is the access token fetch in progress, if any, and the last
// failure, for the configuration with that key.
type oauthState struct {
	key     string
	busy    bool
	problem string
	cancel  context.CancelFunc
}

// oauthConfig is auth's OAuth 2.0 configuration as a request filed under
// collection sends it now.
func (a *app) oauthConfig(auth request.Auth, collection string) (request.Context, request.OAuth2, error) {
	in := a.sendContext(request.Request{Collection: collection, Auth: auth})
	cfg, err := in.OAuth2Config(auth, in.Values(a.activeVars()))
	return in, cfg, err
}

// oauthFields edits an OAuth 2.0 configuration and shows the token it has.
func (a *app) oauthFields(c *ui.Context, auth *request.Auth, collection string) {
	t := c.Theme()
	o := &auth.OAuth2
	if o.Grant == "" {
		o.Grant = request.Grants[0]
	}
	interactive := o.Grant == request.GrantAuthorizationCodePKCE || o.Grant == request.GrantAuthorizationCode
	clientAuth := "Basic header"
	if o.ClientAuth == "Body" {
		clientAuth = "In body"
	}
	ui.Form(c, func() {
		ui.Field(c, "Grant type", func() { ui.Select(c, &o.Grant, request.Grants).Width(240) })
		if interactive {
			ui.Field(c, "Authorization URL", func() {
				ui.TextInput(c, &o.AuthURL).Placeholder("https://login.example.com/authorize")
			})
		}
		ui.Field(c, "Access token URL", func() {
			ui.TextInput(c, &o.TokenURL).Placeholder("https://login.example.com/token")
		})
		ui.Field(c, "Client ID", func() { ui.TextInput(c, &o.ClientID) })
		ui.Field(c, "Client secret", func() {
			ui.TextInput(c, &o.ClientSecret).Placeholder("{{vault:client_secret}}, empty for a public client").Password()
		})
		ui.Field(c, "Scope", func() { ui.TextInput(c, &o.Scope).Placeholder("Space-separated, optional") })
		if o.Grant == request.GrantPassword {
			ui.Field(c, "Username", func() { ui.TextInput(c, &o.Username) })
			ui.Field(c, "Password", func() { ui.TextInput(c, &o.Password).Password() })
		}
		if interactive {
			ui.Field(c, "Callback URL", func() {
				ui.TextInput(c, &o.RedirectURL).Placeholder("http://127.0.0.1 on any free port")
			}).Description("Register it with the provider; the sign-in opens in your browser and returns there.")
		}
		ui.Field(c, "Client authentication", func() {
			if ui.Select(c, &clientAuth, []string{"Basic header", "In body"}).Width(150).Changed() {
				o.ClientAuth = ""
				if clientAuth == "In body" {
					o.ClientAuth = "Body"
				}
			}
		})
		ui.Field(c, "Header prefix", func() { ui.TextInput(c, &o.HeaderPrefix).Placeholder("Bearer").Width(150) })
	})

	in, cfg, err := a.oauthConfig(*auth, collection)
	s := &a.oauth
	status, color := "No access token yet.", t.TextMuted
	if err != nil {
		status, color = err.Error(), t.Danger
	} else if tok, ok := in.Tokens.Peek(cfg); ok {
		switch {
		case tok.Fresh(time.Now()) && tok.Expiry.IsZero():
			status, color = "Access token ready.", t.Success
		case tok.Fresh(time.Now()):
			status, color = "Access token ready · expires "+tok.Expiry.Format("15:04:05"), t.Success
		case tok.Refresh != "":
			status = "Access token expired · the next send refreshes it"
		case !interactive:
			status = "Access token expired · the next send gets a new one"
		default:
			status, color = "Access token expired · get a new one", t.Danger
		}
		if tok.Scope != "" {
			status += " · scope " + tok.Scope
		}
	} else if !interactive {
		status = "No access token yet · the first send gets one"
	}
	if s.key == cfg.Key() && s.busy {
		status, color = "Getting an access token…", t.TextMuted
		if interactive {
			status = "Waiting for the sign-in in your browser…"
		}
	} else if s.key == cfg.Key() && s.problem != "" {
		status, color = s.problem, t.Danger
	}
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, status).FontSize(12).TextColor(color).Grow(1).Selectable()
		busy := s.key == cfg.Key() && s.busy
		if busy && ui.Button(c, "Cancel").Clicked() {
			s.cancel()
		}
		if ui.Button(c, "Clear token").Disabled(err != nil || busy).Clicked() {
			in.Tokens.Clear(cfg)
			s.problem = ""
		}
		if ui.PrimaryButton(c, "Get new access token").Disabled(err != nil || busy).Clicked() {
			a.newAccessToken(in, cfg)
		}
	})
}

// newAccessToken gets a token through the proxy, so the exchange is
// captured too, signing in through the browser when the grant needs it.
func (a *app) newAccessToken(in request.Context, cfg request.OAuth2) {
	s := &a.oauth
	if a.srv == nil || !a.srv.Running() {
		*s = oauthState{key: cfg.Key(), problem: "Start the proxy first: the token request is sent through it."}
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	*s = oauthState{key: cfg.Key(), busy: true, cancel: cancel}
	srv, tokens := a.srv, in.Tokens
	go func() {
		defer cancel()
		_, err := tokens.New(ctx, cfg, request.ProxySender(ctx, srv), mygo.Shell.OpenExternal)
		a.win.Update(func() {
			if s.key != cfg.Key() {
				return
			}
			s.busy = false
			if err != nil {
				s.problem = "No access token: " + err.Error()
				return
			}
			a.setMessage("Got a new access token from " + cfg.TokenURL)
		})
	}()
}
