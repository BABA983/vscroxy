package app

import (
	"cmp"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/google/uuid"
)

type mqttTopicRow struct {
	qos            string
	retainHandling string
	subscriptionID float64
	idText         string
}

type mqttPublishDraft struct {
	message       request.MQTTMessage
	qos           string
	expiry, alias float64
	expiryText    string
	aliasText     string
}

type mqttDocument struct {
	session *mqttSession
	tab     int
	// selected indexes the filtered timeline.
	selected int
	problem  string

	version, willQoS                                 string
	keepAlive, connectTimeout                        float64
	sessionExpiry, receiveMaximum, maximumPacketSize float64
	willDelay, willExpiry                            float64
	keepAliveText, connectTimeoutText                string
	sessionExpiryText, receiveMaximumText            string
	maximumPacketText, willDelayText, willExpiryText string
	topicRows                                        []mqttTopicRow
	// These mirror document values for the widgets. They are written back
	// only when the user changes them, so merely opening a saved document
	// does not dirty it.
	cleanStart, autoReconnect bool
	authType, willType        string
	topicSubscribe            []bool
	synced                    bool

	publish           mqttPublishDraft
	messagePick       string
	messagePickFor    string
	messageIndex      int
	payloadEditor     *editor.State
	payloadFor        string
	payloadType       string
	willPayloadEditor *editor.State
	willPayloadFor    string
	willPayloadType   string

	query       string
	payloadView int
	reader      *editor.State
	readerKey   string
	readerText  string
	split       float32
	list        ui.ListState
	initialized bool
}

func (d *requestDocument) mqttState() *mqttDocument {
	if d.mqttUI == nil {
		d.mqttUI = &mqttDocument{split: 240, messagePick: "New message", messagePickFor: "New message", messageIndex: -1}
	}
	d.ensureMQTT()
	if !d.mqttUI.synced {
		d.mqttUI.authType = cmp.Or(d.auth.Type, "None")
	}
	d.mqttUI.sync(&d.mqttConfig)
	return d.mqttUI
}

// ensureMQTT gives a brand-new document usable defaults. A saved document is
// left exactly as loaded: an empty MQTT 5 client ID is valid (the broker
// assigns one), and MQTT 3.1.1 generates an ID at connect time instead of
// rewriting the document.
func (d *requestDocument) ensureMQTT() {
	c := &d.mqttConfig
	if c.Version != 0 || d.savedKey != "" || d.assetID != "" {
		return
	}
	clean, auto := true, true
	*c = request.MQTTConfig{Version: request.MQTT5, KeepAlive: 60, ConnectTimeoutMs: 10000,
		CleanStart: &clean, AutoReconnect: &auto}
	// A new document starts on the HTTP URL; an MQTT page must not show
	// https: above a broker URL. Only the untouched default changes, so a
	// URL the user clears stays empty.
	if d.composeURL == "https://" {
		d.composeURL = "mqtts://"
	}
}

func newMQTTClientID() string {
	return "vscroxy-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
}

func (u *mqttDocument) stop() {
	if u.session != nil {
		u.session.stop()
	}
}

// stopAndWait is for app quit: give the session a moment to send DISCONNECT,
// so a clean exit does not make the broker publish the will.
func (u *mqttDocument) stopAndWait() {
	if u.session != nil {
		u.session.stopAndWait()
	}
}

func (u *mqttDocument) sync(c *request.MQTTConfig) {
	u.version = request.MQTTVersionLabel(c.Version)
	u.keepAlive, u.connectTimeout = float64(c.KeepAlive), float64(c.ConnectTimeoutMs)
	u.sessionExpiry, u.receiveMaximum = float64(c.Properties.SessionExpiry), float64(c.Properties.ReceiveMaximum)
	u.maximumPacketSize = float64(c.Properties.MaximumPacketSize)
	u.willDelay, u.willExpiry = float64(c.Will.Properties.WillDelay), float64(c.Will.Properties.MessageExpiry)
	if !u.synced {
		u.cleanStart, u.autoReconnect = c.CleanStartEnabled(), c.AutoReconnects()
		u.willType = cmp.Or(c.Will.Type, "text")
		u.keepAliveText, u.connectTimeoutText = strconv.Itoa(c.KeepAlive), strconv.Itoa(c.ConnectTimeoutMs)
		u.sessionExpiryText, u.receiveMaximumText = strconv.Itoa(c.Properties.SessionExpiry), strconv.Itoa(c.Properties.ReceiveMaximum)
		u.maximumPacketText = strconv.Itoa(c.Properties.MaximumPacketSize)
		u.willDelayText, u.willExpiryText = strconv.Itoa(c.Will.Properties.WillDelay), strconv.Itoa(c.Will.Properties.MessageExpiry)
	}
	if u.willQoS == "" {
		u.willQoS = strconv.Itoa(int(c.Will.QoS))
	}
	if len(u.topicRows) != len(c.Topics) {
		u.topicRows = make([]mqttTopicRow, len(c.Topics))
	}
	if len(u.topicSubscribe) != len(c.Topics) {
		u.topicSubscribe = make([]bool, len(c.Topics))
	}
	for i := range c.Topics {
		row := &u.topicRows[i]
		if row.qos == "" {
			row.qos = strconv.Itoa(int(c.Topics[i].QoS))
			row.idText = strconv.Itoa(c.Topics[i].SubscriptionIdentifier)
		}
		if row.retainHandling == "" {
			row.retainHandling = strconv.Itoa(int(c.Topics[i].RetainHandling))
		}
		row.subscriptionID = float64(c.Topics[i].SubscriptionIdentifier)
		if !u.synced {
			u.topicSubscribe[i] = c.Topics[i].Subscribe == nil || *c.Topics[i].Subscribe
		}
	}
	u.synced = true
	if u.publish.qos == "" {
		u.publish.qos = "0"
	}
	if u.publish.message.Type == "" {
		u.publish.message.Type = "text"
	}
	if u.messageIndex >= len(c.Messages) {
		u.messageIndex = -1
	}
	if !u.initialized {
		u.initialized = true
		if len(c.Messages) > 0 {
			u.loadMessage(c.Messages[0], 0)
			u.messagePick = mqttMessageLabel(c.Messages, 0)
			u.messagePickFor = u.messagePick
		}
	}
}

// addTopic appends a filter and its row state.
func (u *mqttDocument) appendTopic(c *request.MQTTConfig) {
	if len(c.Topics) >= 128 {
		return
	}
	subscribe := true
	c.Topics = append(c.Topics, request.MQTTTopic{Name: "", Subscribe: &subscribe})
	u.topicRows = append(u.topicRows, mqttTopicRow{qos: "0", retainHandling: "0", idText: "0"})
	u.topicSubscribe = append(u.topicSubscribe, true)
}

// removeTopic drops one filter and its row state.
func (u *mqttDocument) removeTopic(c *request.MQTTConfig, i int) {
	if i < 0 || i >= len(c.Topics) {
		return
	}
	c.Topics = slices.Delete(c.Topics, i, i+1)
	if i < len(u.topicRows) {
		u.topicRows = slices.Delete(u.topicRows, i, i+1)
	}
	if i < len(u.topicSubscribe) {
		u.topicSubscribe = slices.Delete(u.topicSubscribe, i, i+1)
	}
}

func (u *mqttDocument) newMessage() {
	u.publish = mqttPublishDraft{qos: "0", message: request.MQTTMessage{Type: "text"}, expiryText: "0", aliasText: "0"}
	u.messageIndex = -1
	u.messagePick, u.messagePickFor = "New message", "New message"
	u.payloadEditor, u.payloadFor = nil, ""
}

func (u *mqttDocument) loadMessage(m request.MQTTMessage, index int) {
	m.Properties.UserProperties = slices.Clone(m.Properties.UserProperties)
	u.publish = mqttPublishDraft{message: m, qos: strconv.Itoa(int(m.QoS)),
		expiry: float64(m.Properties.MessageExpiry), alias: float64(m.Properties.TopicAlias),
		expiryText: strconv.Itoa(m.Properties.MessageExpiry), aliasText: strconv.Itoa(m.Properties.TopicAlias)}
	u.messageIndex = index
	u.payloadEditor, u.payloadFor = nil, ""
}

func (u *mqttDocument) draftMessage() request.MQTTMessage {
	m := u.publish.message
	m.QoS = mqttQoS(u.publish.qos)
	m.Properties.MessageExpiry = int(u.publish.expiry)
	m.Properties.TopicAlias = int(u.publish.alias)
	return m
}

func (u *mqttDocument) saveMessage(c *request.MQTTConfig) {
	m := u.draftMessage()
	m.Properties.UserProperties = slices.Clone(m.Properties.UserProperties)
	if u.messageIndex >= 0 && u.messageIndex < len(c.Messages) {
		c.Messages[u.messageIndex] = m
	} else {
		if len(c.Messages) >= 128 {
			return
		}
		c.Messages = append(c.Messages, m)
		u.messageIndex = len(c.Messages) - 1
	}
	u.messagePick = mqttMessageLabel(c.Messages, u.messageIndex)
	u.messagePickFor = u.messagePick
}

func (u *mqttDocument) deleteMessage(c *request.MQTTConfig) {
	if u.messageIndex < 0 || u.messageIndex >= len(c.Messages) {
		return
	}
	c.Messages = slices.Delete(c.Messages, u.messageIndex, u.messageIndex+1)
	u.newMessage()
}

// mqttQoS maps one of the QoS select's fixed values. The select cannot
// produce another value, so there is no out-of-range input to clamp here.
func mqttQoS(value string) byte {
	switch value {
	case "1":
		return 1
	case "2":
		return 2
	default:
		return 0
	}
}

func mqttMessageLabel(messages []request.MQTTMessage, i int) string {
	return fmt.Sprintf("%d · %s", i+1, cmp.Or(messages[i].Name, messages[i].Topic, "Untitled message"))
}

// mqttEditor is the document's own tabs: connection, subscriptions, publish
// drafts and the last will.
func (a *app) mqttEditor(c *ui.Context, t *ui.Theme, d *requestDocument) {
	u := d.mqttState()
	ui.Column(c).Fill().Children(func() {
		ui.Row(c).Height(uikit.HeightTabs).Padding(0, t.Space(3)).Children(func() {
			uikit.Tabs(c, &u.tab, locales.MqttEditorTabPrefix(), locales.Connection(), locales.Subscriptions(), locales.Publish(), locales.LastWill()).Grow(1)
		})
		if u.problem != "" {
			ui.Text(c, u.problem).TextColor(t.Danger).FontSize(uikit.Scaled(uikit.TextMeta)).MaxHeight(55).Padding(0, t.Space(3))
		}
		ui.Column(c).Grow(1).MinHeight(0).Padding(t.Space(2), t.Space(3)).Gap(t.Space(2)).Children(func() {
			switch u.tab {
			case 0:
				a.mqttConnectionTab(c, t, d, u)
			case 1:
				a.mqttSubscriptionsTab(c, t, d, u)
			case 2:
				a.mqttPublishTab(c, t, d, u)
			case 3:
				a.mqttWillTab(c, t, d, u)
			}
		})
	})
}

func (a *app) mqttConnectionTab(c *ui.Context, t *ui.Theme, d *requestDocument, u *mqttDocument) {
	c1 := &d.mqttConfig
	ui.Scroll(c).Grow(1).Gap(t.Space(4)).Padding(0, t.Space(1)).Children(func() {
		uikit.SettingsGroup(c, locales.Broker(), func() {
			uikit.SettingsRow(c, locales.Protocol(), func() ui.Element {
				sel := uikit.Select(c, &u.version, []string{"MQTT 3.1.1", "MQTT 5.0"}).Label(locales.Protocol()).Width(160)
				uikit.Tip(c, sel, locales.PropertiesAndSubscriptionOptionsNeed())
				if sel.Changed() {
					c1.Version = request.MQTT5
					if u.version == "MQTT 3.1.1" {
						c1.Version = request.MQTT311
					}
				}
				return sel
			}, "")
			uikit.SettingsRow(c, locales.ClientId(), func() ui.Element {
				return ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
					in := a.variableInput(c, "mqtt-client-id", &c1.ClientID, "MQTT client ID", d.composeCollection).
						Placeholder(locales.BrokerAssignsOne()).Grow(1).MinWidth(0)
					uikit.Tip(c, in, locales.Mqtt50MayLeave())
					if uikit.SecondaryButton(c, locales.Generate()).Clicked() {
						c1.ClientID = newMQTTClientID()
					}
				})
			}, "")
			uikit.SettingsRow(c, locales.Credentials(), func() ui.Element {
				sel := uikit.SelectLabels(c, &u.authType, []string{"None", "Basic", "Inherit"}, optionLabel).
					Label(locales.MqttCredentials()).Width(160).Height(uikit.ControlHeight)
				uikit.Tip(c, sel, locales.BasicBecomesTheMqttUsername())
				if sel.Changed() {
					d.auth.Type = u.authType
				}
				return sel
			}, "")
			if d.auth.Type == "Basic" {
				uikit.SettingsRow(c, locales.Username(), func() ui.Element {
					return a.variableInput(c, "mqtt-username", &d.auth.Username, "MQTT username", d.composeCollection).Grow(1).MinWidth(0)
				}, "")
				uikit.SettingsRow(c, locales.Password(), func() ui.Element {
					return a.variablePasswordInput(c, "mqtt-password", &d.auth.Password, "MQTT password", d.composeCollection).Grow(1).MinWidth(0)
				}, "")
			}
			uikit.SettingsRow(c, locales.Session(), func() ui.Element {
				return ui.Row(c).Gap(t.Space(3)).AlignItems(ui.Center).Children(func() {
					if ui.Checkbox(c, &u.cleanStart, "Clean start / clean session").Changed() {
						value := u.cleanStart
						c1.CleanStart = &value
					}
					reconnect := ui.Checkbox(c, &u.autoReconnect, "Reconnect after a drop")
					uikit.Tip(c, reconnect, locales.AReconnectReAppliesThe())
					if reconnect.Changed() {
						value := u.autoReconnect
						c1.AutoReconnect = &value
					}
				})
			}, "")
			uikit.SettingsRow(c, locales.KeepAlive(), func() ui.Element {
				return uikit.NumberField(c, &u.keepAliveText, &u.keepAlive, 0, 65535, "s", locales.KeepAliveInSeconds(), "",
					func() { c1.KeepAlive = int(u.keepAlive) })
			}, locales.Seconds0DisablesKeepAlive())
			uikit.SettingsRow(c, locales.ConnectTimeout(), func() ui.Element {
				return uikit.NumberField(c, &u.connectTimeoutText, &u.connectTimeout, 0, 600000, "ms",
					locales.ConnectTimeoutInMilliseconds(), locales.MsForTcpTlsWebsocket(),
					func() { c1.ConnectTimeoutMs = int(u.connectTimeout) })
			}, "")
			uikit.SettingsRow(c, locales.Tls(), func() ui.Element {
				verify := a.inheritance(d.composeCollection).InheritSettings(d.settings).Verifies()
				sw := ui.Switch(c, &verify).Label(locales.VerifyBrokerCertificate())
				uikit.Tip(c, sw, locales.MqttsAndWssVerifyThe())
				if sw.Changed() {
					d.setVerify(verify)
				}
				return sw
			}, "")
			if !a.inheritance(d.composeCollection).InheritSettings(d.settings).Verifies() {
				ui.Text(c, locales.CertificateVerificationIsOffThis3()).
					FontSize(uikit.Scaled(uikit.TextMeta)).TextColor(t.Danger)
			}
		})
		if c1.Version == request.MQTT5 {
			mqttConnectPropertiesEditor(c, t, u, c1)
		}
	})
}

func mqttConnectPropertiesEditor(c *ui.Context, t *ui.Theme, u *mqttDocument, c1 *request.MQTTConfig) {
	uikit.SettingsGroup(c, locales.Mqtt50ConnectProperties(), func() {
		uikit.SettingsRow(c, locales.SessionExpiry(), func() ui.Element {
			return uikit.NumberField(c, &u.sessionExpiryText, &u.sessionExpiry, 0, 4294967295, "s",
				locales.MqttSessionExpiryInSeconds(), "",
				func() { c1.Properties.SessionExpiry = int(u.sessionExpiry) })
		}, locales.Seconds0EndsTheSession())
		uikit.SettingsRow(c, locales.ReceiveMaximum(), func() ui.Element {
			return uikit.NumberField(c, &u.receiveMaximumText, &u.receiveMaximum, 0, 65535, "",
				locales.MqttReceiveMaximum(), locales.N0LeavesTheBrokerS(),
				func() { c1.Properties.ReceiveMaximum = int(u.receiveMaximum) })
		}, "")
		uikit.SettingsRow(c, locales.MaximumPacket(), func() ui.Element {
			return uikit.NumberField(c, &u.maximumPacketText, &u.maximumPacketSize, 0, 268435455, "B",
				locales.MqttMaximumPacketSizeIn(), "",
				func() { c1.Properties.MaximumPacketSize = int(u.maximumPacketSize) })
		}, locales.Bytes0Advertises1Mib())
		uikit.SettingsRow(c, locales.ResponseInformation(), func() ui.Element {
			return ui.Checkbox(c, &c1.Properties.RequestResponseInformation, "Request response information")
		}, "")
		uikit.SettingsRowMultiline(c, locales.UserProperties(), func() ui.Element {
			return ui.Column(c).Children(func() { mqttPropertyEditor(c, t, &c1.Properties.UserProperties) })
		}, "")
	})
}

func (a *app) mqttSubscriptionsTab(c *ui.Context, t *ui.Theme, d *requestDocument, u *mqttDocument) {
	c1 := &d.mqttConfig
	s := u.session.snapshot()
	ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
		if uikit.LinkButton(c, locales.AddTopic()).Clicked() {
			u.appendTopic(c1)
		}
		a.actionButton(c, "request.mqtt-subscribe", locales.SubscribeListed())
		a.actionButton(c, "request.mqtt-unsubscribe", locales.UnsubscribeAll())
		uikit.Meta(c, locales.ActiveSubscriptions(len(s.subscriptions))).Grow(1)
	})
	ui.Scroll(c).Grow(1).Gap(t.Space(2)).Children(func() {
		if len(c1.Topics) == 0 {
			uikit.Meta(c, locales.NoTopicFiltersYet())
		}
		removeTopic := -1
		for i := range c1.Topics {
			topic := &c1.Topics[i]
			row := &u.topicRows[i]
			ui.Column(c).Gap(t.Space(1)).Padding(t.Space(1), t.Space(2)).Radius(uikit.RadiusControl).Background(t.Surface).Children(func() {
				ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
					in := a.variableInput(c, fmt.Sprintf("mqtt-topic-%d", i), &topic.Name, locales.TopicFilter(), d.composeCollection).
						Placeholder(locales.MqttTopicExample()).Grow(1).MinWidth(0)
					uikit.Tip(c, in, locales.TopicFiltersAcceptForOne())
					if uikit.Select(c, &row.qos, []string{"0", "1", "2"}).Label(locales.TopicQos()).Width(64).Changed() {
						topic.QoS = mqttQoS(row.qos)
					}
					if i < len(u.topicSubscribe) {
						if ui.Checkbox(c, &u.topicSubscribe[i], locales.OnConnect()).Changed() {
							value := u.topicSubscribe[i]
							topic.Subscribe = &value
						}
					}
					if uikit.IconButton(c, locales.RemoveTopic(i+1), locales.RemoveThisTopicFilter(), closeIcon).Clicked() {
						removeTopic = i
					}
				})
				ui.Row(c).Gap(t.Space(3)).AlignItems(ui.Center).Children(func() {
					ui.Checkbox(c, &topic.NoLocal, locales.NoLocal())
					ui.Checkbox(c, &topic.RetainAsPublished, locales.RetainAsPublished())
					if c1.Version == request.MQTT5 {
						if uikit.Select(c, &row.retainHandling, []string{"0", "1", "2"}).Label(locales.RetainHandling()).Width(64).Changed() {
							topic.RetainHandling = mqttQoS(row.retainHandling)
						}
					}
				})
				ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
					uikit.Label(c, locales.SubscriptionId())
					uikit.NumberField(c, &row.idText, &row.subscriptionID, 0, 268435455, "",
						locales.SubscriptionIdentifier(), "", func() { topic.SubscriptionIdentifier = int(row.subscriptionID) })
				})
				if c1.Version == request.MQTT5 {
					mqttPropertyEditor(c, t, &topic.UserProperties)
				}
			})
		}
		if removeTopic >= 0 {
			u.removeTopic(c1, removeTopic)
		}
	})
}

func (a *app) mqttPublishTab(c *ui.Context, t *ui.Theme, d *requestDocument, u *mqttDocument) {
	c1 := &d.mqttConfig
	labels := []string{"New message"}
	for i := range c1.Messages {
		labels = append(labels, mqttMessageLabel(c1.Messages, i))
	}
	if u.messagePick != u.messagePickFor {
		u.messagePickFor = u.messagePick
		index := slices.Index(labels, u.messagePick)
		if index <= 0 {
			u.newMessage()
		} else {
			u.loadMessage(c1.Messages[index-1], index-1)
		}
	}
	ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Wrap().Children(func() {
		uikit.Select(c, &u.messagePick, labels).Label(locales.SavedMqttMessage()).Width(180)
		if uikit.CompactButton(c, locales.New()).Clicked() {
			u.newMessage()
		}
		if uikit.CompactButton(c, locales.Save()).Clicked() {
			u.saveMessage(c1)
		}
		if uikit.CompactButton(c, locales.Delete()).Clicked() {
			u.deleteMessage(c1)
		}
		ui.Spacer(c)
		a.actionButton(c, "request.mqtt-publish", "Publish")
	})
	ui.Scroll(c).Grow(1).Gap(t.Space(4)).Padding(0, t.Space(1)).Children(func() {
		uikit.SettingsGroup(c, locales.Message(), func() {
			uikit.SettingsRow(c, locales.Name(), func() ui.Element {
				return uikit.Input(c, &u.publish.message.Name).Placeholder(locales.SavedMessageName()).Label(locales.MessageName())
			}, "")
			uikit.SettingsRow(c, locales.Topic(), func() ui.Element {
				return a.variableInput(c, "mqtt-publish-topic", &u.publish.message.Topic, "Publish topic", d.composeCollection)
			}, "")
			uikit.SettingsRowMultiline(c, locales.Payload(), func() ui.Element {
				return ui.Column(c).Gap(t.Space(2)).Children(func() {
					ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
						if uikit.SelectLabels(c, &u.publish.message.Type, []string{"text", "json", "hex", "base64"}, optionLabel).Label(locales.PayloadEncoding()).Width(120).Changed() {
							u.payloadEditor, u.payloadType = nil, ""
						}
						uikit.Select(c, &u.publish.qos, []string{"0", "1", "2"}).Label(locales.PublishQos()).Width(64)
						ui.Checkbox(c, &u.publish.message.Retain, "Retain")
					})
					language := "text"
					if u.publish.message.Type == "json" {
						language = "json"
					}
					if u.payloadType != language {
						u.payloadType, u.payloadEditor = language, nil
					}
					mqttPayloadEditor(c, &u.publish.message.Payload, &u.payloadEditor, &u.payloadFor, "MQTT publish payload", language, 160)
				})
			}, locales.N1MibMaximum())
		})
		if c1.Version == request.MQTT5 {
			a.mqttPublishPropertiesEditor(c, t, u, d.composeCollection)
		}
	})
}

func (a *app) mqttPublishPropertiesEditor(c *ui.Context, t *ui.Theme, u *mqttDocument, collection string) {
	uikit.SettingsGroup(c, locales.Mqtt50PublishProperties(), func() {
		uikit.SettingsRow(c, locales.Format(), func() ui.Element {
			return ui.Checkbox(c, &u.publish.message.Properties.PayloadFormat, "Payload is UTF-8")
		}, "")
		uikit.SettingsRow(c, locales.ContentType(), func() ui.Element {
			return uikit.Input(c, &u.publish.message.Properties.ContentType).Placeholder("application/json").Label(locales.MimeContentType())
		}, "")
		uikit.SettingsRow(c, locales.ResponseTopic(), func() ui.Element {
			return a.variableInput(c, "mqtt-response-topic", &u.publish.message.Properties.ResponseTopic, "MQTT response topic", collection)
		}, "")
		uikit.SettingsRow(c, locales.CorrelationData(), func() ui.Element {
			return uikit.Input(c, &u.publish.message.Properties.CorrelationData).Placeholder(locales.OpaqueReplyCorrelationValue()).Label(locales.CorrelationData())
		}, "")
		uikit.SettingsRow(c, locales.Expiry(), func() ui.Element {
			return uikit.NumberField(c, &u.publish.expiryText, &u.publish.expiry, 0, 4294967295, "s",
				locales.MessageExpiryInSeconds(), "",
				func() { u.publish.message.Properties.MessageExpiry = int(u.publish.expiry) })
		}, "")
		uikit.SettingsRow(c, locales.TopicAlias(), func() ui.Element {
			return uikit.NumberField(c, &u.publish.aliasText, &u.publish.alias, 0, 65535, "",
				locales.TopicAlias(), "",
				func() { u.publish.message.Properties.TopicAlias = int(u.publish.alias) })
		}, "")
		uikit.SettingsRowMultiline(c, locales.UserProperties(), func() ui.Element {
			return ui.Column(c).Children(func() { mqttPropertyEditor(c, t, &u.publish.message.Properties.UserProperties) })
		}, "")
	})
}

func (a *app) mqttWillTab(c *ui.Context, t *ui.Theme, d *requestDocument, u *mqttDocument) {
	c1 := &d.mqttConfig
	ui.Scroll(c).Grow(1).Gap(t.Space(4)).Padding(0, t.Space(1)).Children(func() {
		ui.Checkbox(c, &c1.Will.Enabled, "Publish a last will")
		ui.Column(c).Disabled(!c1.Will.Enabled).Gap(t.Space(4)).Children(func() {
			uikit.SettingsGroup(c, locales.LastWill(), func() {
				uikit.SettingsRow(c, locales.Topic(), func() ui.Element {
					return a.variableInput(c, "mqtt-will-topic", &c1.Will.Topic, "Last will topic", d.composeCollection)
				}, "")
				uikit.SettingsRowMultiline(c, locales.Payload(), func() ui.Element {
					return ui.Column(c).Gap(t.Space(2)).Children(func() {
						ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
							if uikit.SelectLabels(c, &u.willType, []string{"text", "json", "hex", "base64"}, optionLabel).Label(locales.WillPayloadEncoding()).Width(120).Changed() {
								c1.Will.Type = u.willType
								u.willPayloadEditor, u.willPayloadType = nil, ""
							}
							if uikit.Select(c, &u.willQoS, []string{"0", "1", "2"}).Label(locales.WillQos()).Width(64).Changed() {
								c1.Will.QoS = mqttQoS(u.willQoS)
							}
							ui.Checkbox(c, &c1.Will.Retain, "Retain")
						})
						language := "text"
						if c1.Will.Type == "json" {
							language = "json"
						}
						if u.willPayloadType != language {
							u.willPayloadType, u.willPayloadEditor = language, nil
						}
						mqttPayloadEditor(c, &c1.Will.Payload, &u.willPayloadEditor, &u.willPayloadFor, "MQTT last will payload", language, 140)
					})
				}, "")
			})
			if c1.Version == request.MQTT5 {
				a.mqttWillPropertiesEditor(c, t, d, u)
			}
		})
	})
}

func (a *app) mqttWillPropertiesEditor(c *ui.Context, t *ui.Theme, d *requestDocument, u *mqttDocument) {
	c1 := &d.mqttConfig
	uikit.SettingsGroup(c, locales.Mqtt50WillProperties(), func() {
		uikit.SettingsRow(c, locales.Delay(), func() ui.Element {
			return uikit.NumberField(c, &u.willDelayText, &u.willDelay, 0, 4294967295, "s",
				locales.WillDelayInSeconds(), "",
				func() { c1.Will.Properties.WillDelay = int(u.willDelay) })
		}, "")
		uikit.SettingsRow(c, locales.Expiry(), func() ui.Element {
			return uikit.NumberField(c, &u.willExpiryText, &u.willExpiry, 0, 4294967295, "s",
				locales.WillMessageExpiryInSeconds(), "",
				func() { c1.Will.Properties.MessageExpiry = int(u.willExpiry) })
		}, "")
		uikit.SettingsRow(c, locales.Format(), func() ui.Element {
			return ui.Checkbox(c, &c1.Will.Properties.PayloadFormat, "Payload is UTF-8")
		}, "")
		uikit.SettingsRow(c, locales.ContentType(), func() ui.Element {
			return uikit.Input(c, &c1.Will.Properties.ContentType).Placeholder("application/json").Label(locales.WillMimeContentType())
		}, "")
		uikit.SettingsRow(c, locales.ResponseTopic(), func() ui.Element {
			return a.variableInput(c, "mqtt-will-response", &c1.Will.Properties.ResponseTopic, "Will response topic", d.composeCollection)
		}, "")
		uikit.SettingsRow(c, locales.CorrelationData(), func() ui.Element {
			return uikit.Input(c, &c1.Will.Properties.CorrelationData).Placeholder(locales.OpaqueReplyCorrelationValue()).Label(locales.WillCorrelationData())
		}, "")
		uikit.SettingsRowMultiline(c, locales.UserProperties(), func() ui.Element {
			return ui.Column(c).Children(func() { mqttPropertyEditor(c, t, &c1.Will.Properties.UserProperties) })
		}, "")
	})
}

// mqttPropertyEditor edits ordered key/value user properties.
func mqttPropertyEditor(c *ui.Context, t *ui.Theme, props *[]request.MQTTProperty) {
	for i := 0; i < len(*props); i++ {
		property := &(*props)[i]
		removed := false
		ui.Row(c).Gap(t.Space(1)).AlignItems(ui.Center).Children(func() {
			uikit.Input(c, &property.Key).Placeholder("key").Label(locales.PropertyKey()).Grow(1).MinWidth(0)
			uikit.Input(c, &property.Value).Placeholder("value").Label(locales.PropertyValue()).Grow(1).MinWidth(0)
			if uikit.IconButton(c, locales.RemoveProperty(i+1), locales.RemoveThisUserProperty(), closeIcon).Clicked() {
				*props = slices.Delete(*props, i, i+1)
				removed = true
			}
		})
		if removed {
			break
		}
	}
	if len(*props) < 64 {
		ui.Row(c).AlignItems(ui.Center).Children(func() {
			if uikit.LinkButton(c, locales.AddProperty(), plusIcon).Clicked() {
				*props = append(*props, request.MQTTProperty{})
			}
		})
	}
}

// mqttResponse is the live timeline: connection events, sent and received
// messages, their properties and a bounded payload view.
func (a *app) mqttResponse(c *ui.Context, t *ui.Theme, d *requestDocument) {
	u := d.mqttState()
	s := u.session.snapshot()
	ui.Column(c).Fill().Children(func() {
		ui.Row(c).Height(uikit.HeightTabs).Padding(0, t.Space(3)).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
			ui.Text(c, locales.MqttMessages()).FontSize(uikit.Scaled(uikit.TextBody)).Grow(1)
			if u.session != nil {
				uikit.Meta(c, stateLabel(s.state)).SingleLine()
			}
		})
		if u.session == nil {
			uikit.EmptyState(c, locales.NoMqttConnectionYet(), locales.ConnectToPublishAndSubscribe(), nil)
			return
		}
		if s.problem != "" {
			ui.Text(c, s.problem).TextColor(t.Danger).FontSize(uikit.Scaled(uikit.TextMeta)).MaxHeight(55).Padding(0, t.Space(3))
		}
		ui.Row(c).Padding(t.Space(1), t.Space(3)).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
			uikit.Input(c, &u.query).Label(locales.SearchMqttMessages()).Placeholder(locales.SearchTopicPayloadOrProperties()).Grow(1).MinWidth(0)
			uikit.Meta(c, locales.RetainedPreview3(len(s.events), s.evicted, s.dropped)).SingleLine()
		})
		query := strings.ToLower(strings.TrimSpace(u.query))
		var events []mqttEvent
		for _, e := range s.events {
			if query == "" {
				events = append(events, e)
				continue
			}
			haystack := e.direction + " " + e.name + " " + e.note + " " + e.properties
			if e.direction != "System" {
				haystack += " " + string(e.payload)
			}
			if strings.Contains(strings.ToLower(haystack), query) {
				events = append(events, e)
			}
		}
		if len(events) == 0 {
			if strings.TrimSpace(u.query) != "" {
				uikit.EmptyState(c, locales.NoMatchingMqttMessages(), locales.ChangeTheSearchOrWait2(), nil)
			} else {
				uikit.EmptyState(c, locales.NoMqttMessagesYet(), locales.PublishOrWaitForThe(), nil)
			}
			return
		}
		ui.Split(c, &u.split, func() {
			u.list.Selected = &u.selected
			u.list.Key = func(i int) any { return events[i].seq }
			u.list.Label = func(i int) string { return fmt.Sprintf("MQTT event %d %s", events[i].seq, events[i].name) }
			uikit.DataListIndexed(c, &u.list, len(events), func(i int) {
				e := events[i]
				ui.Column(c).Padding(t.Space(1), t.Space(2)).Children(func() {
					ui.Text(c, e.direction+" · "+e.name).FontSize(uikit.Scaled(uikit.TextBody)).SingleLine()
					uikit.Meta(c, mqttEventMeta(e)).SingleLine()
				})
			}).Grow(1)
		}, func() {
			// A selected key the list no longer has comes back as -1: the event
			// went away, and the detail says nothing is selected rather than
			// showing the oldest one left.
			settleEvent(&u.selected, len(events))
			e := events[u.selected]
			key := fmt.Sprintf("%d/%d", e.seq, u.payloadView)
			if u.readerKey != key {
				u.readerKey = key
				u.readerText = mqttEventText(e, u.payloadView)
				u.reader = &editor.State{Buffer: editor.NewBuffer(u.readerText), ReadOnly: true, NoScrollPastEnd: true, SoftWrap: true, Label: locales.MqttMessage()}
			}
			ui.Column(c).Fill().Children(func() {
				ui.Row(c).Padding(t.Space(1), t.Space(2)).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
					uikit.Meta(c, e.direction+" · "+e.name).Grow(1).SingleLine()
					if e.direction != "System" {
						uikit.Tabs(c, &u.payloadView, "MQTT payload view", locales.Text(), locales.Json(), locales.Hex()).Width(170)
					}
					services, text := c.Services(), u.readerText
					uikit.SecondaryButton(c, locales.CopyShownMessage()).OnClick(func() { services.WriteClipboard(text) })
				})
				if e.properties != "" {
					ui.Text(c, e.properties).Font(uikit.FontMono).FontSize(uikit.Scaled(uikit.TextMeta)).TextColor(t.TextMuted).MaxHeight(90).Padding(0, t.Space(2))
				}
				requestCodeEditor(c, u.reader)
			})
		}).Grow(1)
	})
}

// mqttPayloadEditor is the authored payload editor with a fixed height, for a
// form that scrolls instead of giving its children the remaining height.
func mqttPayloadEditor(c *ui.Context, text *string, state **editor.State, seen *string, label, language string, height float32) {
	if *state == nil || *seen != *text {
		buf := editor.NewBuffer(*text)
		if language == "json" {
			buf.SetLanguage(editor.LanguageNamed("json"))
		}
		*state = &editor.State{Buffer: buf, NoScrollPastEnd: true, SoftWrap: true, Label: label}
		*seen = *text
	}
	ui.Box(c).Height(height).FillWidth().Children(func() { requestCodeEditor(c, *state) })
	if current := (*state).Buffer.String(); current != *text {
		*text, *seen = current, current
		c.Invalidate()
	}
}

func mqttEventMeta(e mqttEvent) string {
	if e.direction == "System" {
		return fmt.Sprintf("#%d · %s", e.seq, e.at.Format("15:04:05.000"))
	}
	flags := []string{fmt.Sprintf("QoS %d", e.qos)}
	if e.retain {
		flags = append(flags, "retain")
	}
	if e.note != "" {
		flags = append(flags, e.note)
	}
	return fmt.Sprintf("#%d · %s · %s · %d B", e.seq, e.at.Format("15:04:05.000"), strings.Join(flags, " · "), e.size)
}

func mqttEventText(e mqttEvent, view int) string {
	if e.direction == "System" {
		return e.note
	}
	payload := e.payload
	text := string(payload)
	valid := utf8.Valid(payload)
	switch view {
	case 1:
		if formatted, ok := indentJSON(text); ok {
			if len(payload) < e.size {
				formatted += fmt.Sprintf("\nRetained %d of %d bytes", len(payload), e.size)
			}
			return formatted
		}
		return text
	case 2:
		if len(payload) < e.size {
			return hex.Dump(payload) + fmt.Sprintf("\nRetained %d of %d bytes", len(payload), e.size)
		}
		return hex.Dump(payload)
	default:
		if !valid {
			return "Not UTF-8; shown as hex\n" + hex.Dump(payload)
		}
		if len(payload) < e.size {
			text += fmt.Sprintf("\nRetained %d of %d bytes", len(payload), e.size)
		}
		return text
	}
}

func (a *app) drainMQTT() {
	for _, d := range a.documents {
		u := d.mqttUI
		if d.protocol == "MQTT" && u != nil && u.session != nil && u.session.snapshot().finished {
			d.composeWaiting, d.sendCancel, d.sendCancelling = false, nil, false
		}
	}
}

func (a *app) connectMQTT(d *requestDocument) {
	u := d.mqttState()
	if u.session != nil && !u.session.snapshot().finished {
		return
	}
	r := d.value().Request
	// Saved publish drafts are validated when they are sent, not when the
	// broker connection opens.
	r.MQTT.Messages = nil
	// 3.1.1 needs a client ID, but the generated one belongs to this
	// connection only; the saved document keeps its empty field. A persistent
	// 3.1.1 session must use an explicit ID, so leave that to Validate.
	if r.MQTT.Version == request.MQTT311 && r.MQTT.ClientID == "" && r.MQTT.CleanStartEnabled() {
		r.MQTT.ClientID = newMQTTClientID()
	}
	in, vars := a.sendContext(r), a.activeVars()
	prepared, cfg, err := r.PrepareMQTT(in, vars)
	if err != nil {
		u.problem = err.Error()
		return
	}
	address, secure, err := request.MQTTEndpoint(prepared.URL)
	if err != nil {
		u.problem = err.Error()
		return
	}
	websocket := strings.HasPrefix(strings.ToLower(prepared.URL), "ws://") ||
		strings.HasPrefix(strings.ToLower(prepared.URL), "wss://")
	// A WebSocket upgrade is HTTP, so it follows the running capture proxy
	// like gRPC; raw MQTT TCP connects directly. Evaluate both on every dial,
	// so a reconnect follows the proxy's current state.
	proxy := func() (string, []byte) {
		if !websocket || a.srv == nil || !a.srv.Running() {
			return "", nil
		}
		return a.srv.Addr(), a.srv.CACertificate()
	}
	verify := in.InheritSettings(r.Settings).Verifies()
	tlsConfig := func(proxyCA []byte) (*tls.Config, error) {
		if !secure {
			return nil, nil
		}
		// Read the certificate files for every connect and reconnect, as the
		// other protocol clients do. proxyCA trusts the capture proxy's
		// interception certificate for wss.
		return in.Certificates.TLSConfig(address, proxyCA, !verify)
	}
	u.session = openMQTTSession(prepared.URL, r, in, vars, cfg, proxy, verify, tlsConfig, a.invalidate)
	u.problem, u.query, u.readerKey, u.reader, u.selected = "", "", "", nil, 0
	u.list = ui.ListState{}
	d.composeWaiting, d.sendCancel, d.sendCancelling = true, u.session.cancel, false
	d.historyPick, d.comparing = 0, false
}

func (a *app) publishMQTT(d *requestDocument) {
	u := d.mqttState()
	s := u.session
	if s == nil {
		return
	}
	r := s.input
	r.MQTT.Messages = []request.MQTTMessage{u.draftMessage()}
	r.MQTT.Topics = nil
	_, cfg, err := r.PrepareMQTT(s.scope, s.vars)
	if err != nil {
		u.problem = err.Error()
		return
	}
	if len(cfg.Messages) != 1 {
		return
	}
	m := cfg.Messages[0]
	payload, err := request.MQTTPayload(m.Payload, m.Type)
	if err != nil {
		u.problem = err.Error()
		return
	}
	u.problem = ""
	s.publish(request.MQTTPublish{Topic: m.Topic, Payload: payload, QoS: m.QoS, Retain: m.Retain, Properties: m.Properties},
		cmp.Or(m.Name, m.Topic))
}

func (a *app) subscribeMQTT(d *requestDocument) {
	u := d.mqttState()
	s := u.session
	if s == nil {
		return
	}
	r := s.input
	r.MQTT.Topics = d.mqttConfig.Topics
	_, cfg, err := r.PrepareMQTT(s.scope, s.vars)
	if err != nil {
		u.problem = err.Error()
		return
	}
	subs := cfg.TopicsToSubscribe()
	if len(subs) == 0 {
		u.problem = "Add at least one topic filter, or turn on at least one topic's On connect"
		return
	}
	u.problem = ""
	s.subscribe(subs)
}

func (a *app) unsubscribeMQTT(d *requestDocument) {
	if u := d.mqttUI; u != nil && u.session != nil {
		u.session.unsubscribeAll()
	}
}

func (a *app) mqttActions(d *requestDocument, editing bool) []action {
	ready := editing && d.protocol == "MQTT"
	var u *mqttDocument
	var s mqttSnapshot
	if ready {
		u = d.mqttState()
		s = u.session.snapshot()
	}
	live := ready && s.ready && !s.busy && !d.sendCancelling
	return []action{
		do("request.mqtt-publish", locales.PublishMqttMessage(), func() { a.publishMQTT(d) }).when(live, "Connect MQTT first"),
		do("request.mqtt-subscribe", locales.SubscribeMqttTopics(), func() { a.subscribeMQTT(d) }).when(live, "Connect MQTT first"),
		do("request.mqtt-unsubscribe", locales.UnsubscribeMqttTopics(), func() { a.unsubscribeMQTT(d) }).when(live && len(s.subscriptions) > 0, "No active MQTT subscriptions"),
	}
}
