package request

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestPrepareMQTTExpandsVariablesAndVault(t *testing.T) {
	r := Request{
		Protocol: "MQTT",
		URL:      "mqtts://{{broker}}",
		Auth:     Auth{Type: "Basic", Username: "{{user}}", Password: "{{vault:mqtt-pass}}"},
		MQTT: MQTTConfig{
			Version:  5,
			ClientID: "{{client}}",
			Properties: MQTTConnectProperties{
				UserProperties: []MQTTProperty{{Key: "app", Value: "{{app}}"}},
			},
			Topics: []MQTTTopic{{Name: "{{prefix}}/+/temp", QoS: 1, Description: "{{prefix}} sensors"}},
			Messages: []MQTTMessage{{Name: "out", Topic: "{{prefix}}/out", Payload: `{"at":"{{at}}"}`,
				Type: "json", QoS: 1, Properties: MQTTMessageProperties{ResponseTopic: "{{prefix}}/reply"}}},
		},
	}
	var vaultHost, vaultName string
	in := Context{Vault: func(name, host string) (string, error) {
		vaultHost, vaultName = host, name
		if host != "broker.test" {
			return "", errors.New("host not authorized")
		}
		return "secret", nil
	}}
	vars := map[string]string{"broker": "broker.test:8883", "client": "c1", "prefix": "shop", "user": "demo", "app": "vscroxy", "at": "10:00"}
	prepared, cfg, err := r.PrepareMQTT(in, vars)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.URL != "mqtts://broker.test:8883" || cfg.ClientID != "c1" || cfg.Username != "demo" || cfg.Password != "secret" {
		t.Fatalf("prepared = %q, cfg = %+v", prepared.URL, cfg)
	}
	if vaultHost != "broker.test" || vaultName != "mqtt-pass" {
		t.Fatalf("vault read for %q at %q", vaultName, vaultHost)
	}
	if cfg.Topics[0].Name != "shop/+/temp" || cfg.Topics[0].Description != "shop sensors" {
		t.Fatalf("topic = %+v", cfg.Topics[0])
	}
	if cfg.Messages[0].Topic != "shop/out" || cfg.Messages[0].Payload != `{"at":"10:00"}` || cfg.Messages[0].Properties.ResponseTopic != "shop/reply" {
		t.Fatalf("message = %+v", cfg.Messages[0])
	}
	if cfg.Properties.UserProperties[0].Value != "vscroxy" {
		t.Fatalf("connect user properties = %+v", cfg.Properties.UserProperties)
	}
	// The authored document keeps its placeholders.
	if r.MQTT.Messages[0].Topic != "{{prefix}}/out" {
		t.Fatalf("PrepareMQTT mutated the authored request: %+v", r.MQTT)
	}
}

func TestPrepareMQTTExpandsDynamicVariables(t *testing.T) {
	r := Request{Protocol: "MQTT", URL: "mqtt://broker.test:1883", MQTT: MQTTConfig{
		Version: 4, ClientID: "{{$randomFirstName}}",
		Topics:   []MQTTTopic{{Name: "{{$randomLastName}}/#"}},
		Messages: []MQTTMessage{{Topic: "{{$randomFirstName}}/out", Payload: "{{$randomFirstName}}"}},
	}}
	_, cfg, err := r.PrepareMQTT(Context{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(cfg.ClientID, "{{") || strings.Contains(cfg.Topics[0].Name, "{{") || strings.Contains(cfg.Messages[0].Topic, "{{") {
		t.Fatalf("dynamic variables were not expanded: %+v", cfg)
	}
	if strings.Contains(r.MQTT.ClientID, "vscroxy") {
		t.Fatal("PrepareMQTT mutated the authored request")
	}
}

func TestPrepareMQTTRejectsVaultForAnotherHost(t *testing.T) {
	r := Request{Protocol: "MQTT", URL: "mqtt://broker.test:1883", Auth: Auth{Type: "Basic", Username: "demo", Password: "{{vault:pass}}"},
		MQTT: MQTTConfig{Version: 4}}
	in := Context{Vault: func(name, host string) (string, error) { return "", errors.New("host not authorized") }}
	_, _, err := r.PrepareMQTT(in, nil)
	if err == nil || !strings.Contains(err.Error(), "host not authorized") {
		t.Fatalf("err = %v", err)
	}
}

func TestPrepareMQTTRejectsHTTPExecution(t *testing.T) {
	base := Request{Protocol: "MQTT", URL: "mqtt://broker.test:1883", MQTT: MQTTConfig{Version: 4}}
	for name, edit := range map[string]func(*Request){
		"pre":    func(r *Request) { r.Pre = "pm.test('x')" },
		"post":   func(r *Request) { r.Post = "pm.test('x')" },
		"checks": func(r *Request) { r.Checks = Checks{Status: "200"} },
		"tokens": func(r *Request) { r.TokenSources = []TokenSource{{Environment: "e", Variable: "v"}} },
		"folder": func(r *Request) { r.Collection = "Shop" },
	} {
		t.Run(name, func(t *testing.T) {
			r := base
			edit(&r)
			in := Context{}
			if name == "folder" {
				in.Folders = []Folder{{Path: "Shop", Pre: "pm.test('x')"}}
			}
			if _, _, err := r.PrepareMQTT(in, nil); err == nil {
				t.Fatal("MQTT accepted HTTP scripts or checks")
			}
		})
	}
}

func TestPrepareMQTTAuthRules(t *testing.T) {
	base := Request{Protocol: "MQTT", URL: "mqtt://broker.test:1883", MQTT: MQTTConfig{Version: 4}}
	r := base
	r.Auth = Auth{Type: "Bearer", Token: "x"}
	if _, _, err := r.PrepareMQTT(Context{}, nil); err == nil {
		t.Fatal("Bearer auth accepted for MQTT")
	}
	// An inherited HTTP auth is not silently used as MQTT credentials.
	r = base
	r.Auth = Auth{Type: AuthInherit}
	in := Context{Folders: []Folder{{Path: "Shop", Auth: Auth{Type: "Bearer", Token: "x"}}}}
	if _, cfg, err := r.PrepareMQTT(in, nil); err != nil || cfg.Username != "" || cfg.Password != "" {
		t.Fatalf("inherited Bearer = %+v, %v", cfg, err)
	}
	// An inherited Basic auth is the credential set.
	r = base
	r.Auth = Auth{Type: AuthInherit}
	in = Context{Folders: []Folder{{Path: "Shop", Auth: Auth{Type: "Basic", Username: "demo", Password: "secret"}}}}
	if _, cfg, err := r.PrepareMQTT(in, nil); err != nil || cfg.Username != "demo" || cfg.Password != "secret" {
		t.Fatalf("inherited Basic = %+v, %v", cfg, err)
	}
}

func TestMQTTValidateVersionProperties(t *testing.T) {
	base := MQTTConfig{Version: 4, ClientID: "c", Topics: []MQTTTopic{{Name: "a/#"}}, Messages: []MQTTMessage{{Topic: "a/b"}}}
	if err := base.Validate(); err != nil {
		t.Fatalf("valid 3.1.1 document: %v", err)
	}
	cases := map[string]func(*MQTTConfig){
		"connect": func(c *MQTTConfig) { c.Properties.SessionExpiry = 10 },
		"will": func(c *MQTTConfig) {
			c.Will = MQTTWill{Enabled: true, Topic: "a/will", Properties: MQTTWillProperties{ContentType: "text/plain"}}
		},
		"topic":   func(c *MQTTConfig) { c.Topics[0].NoLocal = true },
		"message": func(c *MQTTConfig) { c.Messages[0].Properties.ResponseTopic = "a/reply" },
	}
	for name, edit := range cases {
		t.Run(name, func(t *testing.T) {
			c := base
			c.Topics = append([]MQTTTopic(nil), base.Topics...)
			c.Messages = append([]MQTTMessage(nil), base.Messages...)
			edit(&c)
			if err := c.Validate(); err == nil {
				t.Fatal("MQTT 5 field accepted for 3.1.1")
			}
		})
	}
	base.Version = 5
	if err := base.Validate(); err != nil {
		t.Fatalf("valid 5.0 document: %v", err)
	}
}

func TestMQTTConfigDefaults(t *testing.T) {
	var c MQTTConfig
	if !c.CleanStartEnabled() || !c.AutoReconnects() {
		t.Fatal("zero config must default to clean start and auto reconnect")
	}
	if c.ConnectTimeout() != 10*time.Second {
		t.Fatalf("default connect timeout = %v", c.ConnectTimeout())
	}
	no := false
	c.CleanStart, c.AutoReconnect = &no, &no
	if c.CleanStartEnabled() || c.AutoReconnects() {
		t.Fatal("explicit false was not honored")
	}
}

func TestMQTTTopicsToSubscribe(t *testing.T) {
	no := false
	c := MQTTConfig{Topics: []MQTTTopic{{Name: "a/#", QoS: 1}, {Name: "b/#", QoS: 2, Subscribe: &no}}}
	subs := c.TopicsToSubscribe()
	if len(subs) != 1 || subs[0].Topic != "a/#" || subs[0].QoS != 1 {
		t.Fatalf("subscriptions = %+v", subs)
	}
}
