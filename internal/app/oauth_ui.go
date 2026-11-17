package app

import (
	"context"
	"github.com/baba983/vscroxy/internal/uikit"
	"time"

	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/locales"
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

// oauthFields adds OAuth 2.0 fields to the authorization form and shows the
// token it has; the common form keeps all authorization labels aligned.
func (a *app) oauthFields(c *ui.Context, auth *request.Auth, collection string) {
	t := c.Theme()
	o := &auth.OAuth2
	if o.Grant == "" {
		o.Grant = request.Grants[0]
	}
	interactive := o.Grant == request.GrantAuthorizationCodePKCE || o.Grant == request.GrantAuthorizationCode || o.Grant == request.GrantImplicit
	clientAuth := "Basic header"
	if o.ClientAuth == "Body" {
		clientAuth = "In body"
	}
	input := func(value *string, label string) ui.Element {
		return a.variableInput(c, "oauth-"+label, value, label, collection)
	}
	password := func(value *string, label string) ui.Element {
		return a.variablePasswordInput(c, "oauth-"+label, value, label, collection)
	}
	ui.Field(c, locales.GrantType(), func() { uikit.Select(c, &o.Grant, request.Grants).Width(uikit.WidthWide) })
	if interactive {
		ui.Field(c, locales.AuthorizationUrl(), func() {
			input(&o.AuthURL, "Authorization URL").Placeholder("https://login.example.com/authorize")
		})
	}
	if o.Grant != request.GrantImplicit {
		ui.Field(c, locales.AccessTokenUrl(), func() {
			input(&o.TokenURL, "Access token URL").Placeholder("https://login.example.com/token")
		})
	}
	ui.Field(c, locales.ClientId(), func() { input(&o.ClientID, "Client ID") })
	ui.Field(c, locales.ClientSecret(), func() {
		password(&o.ClientSecret, "Client secret").Placeholder("{{vault:client_secret}}, empty for a public client")
	})
	ui.Field(c, locales.Scope(), func() { input(&o.Scope, "Scope").Placeholder(locales.SpaceSeparatedOptional()) })
	if o.Grant == request.GrantPassword {
		ui.Field(c, locales.Username(), func() { input(&o.Username, "Username") })
		ui.Field(c, locales.Password(), func() { password(&o.Password, "Password") })
	}
	if interactive {
		ui.Field(c, locales.CallbackUrl(), func() {
			input(&o.RedirectURL, "Callback URL").Placeholder("http://127.0.0.1 on any free port")
		}).Description(locales.RegisterItWithTheProvider())
	}
	ui.Field(c, locales.ClientAuthentication(), func() {
		if uikit.SelectLabels(c, &clientAuth, []string{"Basic header", "In body"}, optionLabel).Width(uikit.WidthField).Changed() {
			o.ClientAuth = ""
			if clientAuth == "In body" {
				o.ClientAuth = "Body"
			}
		}
	})
	ui.Field(c, locales.HeaderPrefix(), func() { input(&o.HeaderPrefix, "Header prefix").Placeholder(locales.Bearer()).Width(150) })

	in, cfg, err := a.oauthConfig(*auth, collection)
	s := &a.oauth
	status, color := locales.NoAccessTokenYet(), t.TextMuted
	if err != nil {
		status, color = err.Error(), t.Danger
	} else if tok, ok := in.Tokens.Peek(cfg); ok {
		switch {
		case tok.Fresh(time.Now()) && tok.Expiry.IsZero():
			status, color = locales.AccessTokenReady(), t.Success
		case tok.Fresh(time.Now()):
			status, color = locales.AccessTokenReadyExpires(tok.Expiry.Format("15:04:05")), t.Success
		case tok.Refresh != "":
			status = locales.AccessTokenExpiredTheNext2()
		case !interactive:
			status = locales.AccessTokenExpiredTheNext()
		default:
			status, color = locales.AccessTokenExpiredGetA(), t.Danger
		}
		if tok.Scope != "" {
			status += locales.ScopeNote(tok.Scope)
		}
	} else if !interactive {
		status = locales.NoAccessTokenYetThe()
	}
	if s.key == cfg.Key() && s.busy {
		status, color = locales.GettingAnAccessToken(), t.TextMuted
		if interactive {
			status = locales.WaitingForTheSignIn()
		}
	} else if s.key == cfg.Key() && s.problem != "" {
		status, color = s.problem, t.Danger
	}
	ui.Field(c, locales.AccessToken(), func() {
		uikit.Caption(c, status).TextColor(color).Selectable()
		ui.Row(c).Gap(t.Space(uikit.SpaceM)).AlignItems(ui.Center).Children(func() {
			busy := s.key == cfg.Key() && s.busy
			if busy && uikit.SecondaryButton(c, locales.Cancel()).Clicked() {
				s.cancel()
			}
			if uikit.SecondaryButton(c, locales.ClearToken()).Disabled(err != nil || busy).Clicked() {
				in.Tokens.Clear(cfg)
				s.problem = ""
			}
			if uikit.SecondaryButton(c, locales.GetNewAccessToken()).Disabled(err != nil || busy).Clicked() {
				a.newAccessToken(in, cfg)
			}
		})
	})
}

// savedOAuthTokens lists the tokens kept sealed on this machine. A refresh
// uses the token's saved configuration; delete removes only the token.
func (a *app) savedOAuthTokens(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).FillWidth().Gap(t.Space(uikit.SpaceXS)).Children(func() {
		uikit.BodyStrong(c, locales.SavedAccessTokens()).TextColor(t.Text)
		if err := a.ensureOAuthTokens(); err != nil {
			uikit.ErrorText(c, locales.TokenStoreProblem(err.Error())).MaxLines(3)
			return
		}
		tokens := a.tokens.All()
		if len(tokens) == 0 {
			uikit.Meta(c, locales.NoSavedAccessTokens())
			return
		}
		for _, tok := range tokens {
			c.Key("oauth-token-" + tok.Key)
			ui.Column(c).FillWidth().Gap(t.Space(uikit.SpaceHair)).Padding(t.Space(uikit.SpaceXS), 0).Children(func() {
				ui.Row(c).Gap(t.Space(uikit.SpaceM)).AlignItems(ui.Center).Children(func() {
					uikit.Body(c, cmpOr(tok.Label, tok.Key)).TextColor(t.Text).Grow(1).SingleLine()
					uikit.Meta(c, maskToken(tok.Access)).Font(uikit.FontMono)
				})
				ui.Row(c).Gap(t.Space(uikit.SpaceM)).AlignItems(ui.Center).Children(func() {
					uikit.Meta(c, tokenStatus(tok)).Grow(1).SingleLine()
					busy := a.oauth.key == tok.Key && a.oauth.busy
					if uikit.SecondaryButton(c, locales.Refresh()).Disabled(busy).Clicked() {
						a.refreshSavedToken(tok.Key)
					}
					if uikit.SecondaryButton(c, locales.Delete()).Disabled(busy).Clicked() {
						a.deleteSavedToken(tok.Key)
					}
				})
			})
		}
	})
}

func tokenStatus(tok request.Token) string {
	now := time.Now()
	switch {
	case tok.Fresh(now) && tok.Expiry.IsZero():
		return locales.TokenReady()
	case tok.Fresh(now):
		return locales.TokenExpiresAt(tok.Expiry.Format("15:04:05"))
	case tok.Refresh != "":
		return locales.TokenExpiredRefreshAvailable()
	default:
		return locales.TokenExpired()
	}
}

func maskToken(access string) string {
	if len(access) <= 8 {
		return "…"
	}
	return access[:4] + "…" + access[len(access)-4:]
}

// refreshSavedToken renews one kept token. The token's saved configuration
// supplies the client and URLs; an interactive token may open the browser.
func (a *app) refreshSavedToken(key string) {
	var found request.Token
	ok := false
	for _, t := range a.tokens.All() {
		if t.Key == key {
			found, ok = t, true
			break
		}
	}
	if !ok {
		return
	}
	if a.srv == nil || !a.srv.Running() {
		a.warn(locales.TokenRequestThroughProxy())
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	s := &a.oauth
	*s = oauthState{key: key, busy: true, cancel: cancel}
	srv := a.srv
	refresh := func() {
		_, err := a.tokens.Refresh(ctx, key, request.ProxySender(ctx, srv), mygo.Shell.OpenExternal)
		apply := func() {
			if s.key != key {
				return
			}
			s.busy = false
			if err != nil {
				s.problem = locales.TokenRefreshFailed(err.Error())
				return
			}
			a.done(locales.TokenRefreshed(found.Label))
		}
		if a.win == nil {
			apply()
			return
		}
		a.win.Update(apply)
	}
	if a.win == nil {
		defer cancel()
		refresh()
		return
	}
	go func() { defer cancel(); refresh() }()
}

func (a *app) deleteSavedToken(key string) {
	if !a.tokens.Delete(key) {
		return
	}
	if a.oauth.key == key {
		a.oauth = oauthState{}
	}
	a.done(locales.TokenDeleted())
}

// newAccessToken gets a token through the proxy, so the exchange is
// captured too, signing in through the browser when the grant needs it.
func (a *app) newAccessToken(in request.Context, cfg request.OAuth2) {
	s := &a.oauth
	if a.srv == nil || !a.srv.Running() {
		*s = oauthState{key: cfg.Key(), problem: locales.TokenRequestThroughProxy()}
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
				s.problem = locales.NoAccessTokenWith(err.Error())
				return
			}
			a.done(locales.NewAccessToken(cfg.TokenURL))
		})
	}()
}
