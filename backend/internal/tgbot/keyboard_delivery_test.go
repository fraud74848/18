package tgbot

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestHelpAndStartReplyKeyboardDelivery(t *testing.T) {
	for _, command := range []string{"/help", "/start", "/help@test_bot", "/start@test_bot"} {
		t.Run(command, func(t *testing.T) {
			old := http.DefaultClient
			defer func() { http.DefaultClient = old }()
			sent := 0
			http.DefaultClient = &http.Client{Transport: menuTransport(func(r *http.Request) (*http.Response, error) {
				body := `{"code":0,"data":{"replyKeyboard":{"keyboard":[[{"text":"上班"},{"text":"下班"}]],"selective":true,"is_persistent":true}}}`
				if r.URL.Host == "api.telegram.org" {
					sent++
					var payload map[string]interface{}
					if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
						t.Fatal(err)
					}
					reply, ok := payload["reply_parameters"].(map[string]interface{})
					if !ok || reply["message_id"] != float64(42) {
						t.Fatalf("missing reply target: %#v", payload)
					}
					kb, ok := payload["reply_markup"].(map[string]interface{})
					if !ok || kb["keyboard"] == nil || kb["is_persistent"] != true {
						t.Fatalf("missing persistent keyboard: %#v", payload)
					}
					body = `{"ok":true,"result":{"message_id":43}}`
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
			})}
			msg := &tgMessage{MessageID: 42, Text: command}
			msg.Chat.ID = -100123
			msg.Chat.Type = "supergroup"
			msg.From.ID = 123
			if !handleSlashCommand(config{BotToken: "test", BackendURL: "http://backend"}, msg) || sent != 1 {
				t.Fatalf("sent %d messages", sent)
			}
		})
	}
}

func TestHelpDeliveryFailureIsNotSilentlySuccessful(t *testing.T) {
	old := http.DefaultClient
	defer func() { http.DefaultClient = old }()
	http.DefaultClient = &http.Client{Transport: menuTransport(func(r *http.Request) (*http.Response, error) {
		body := `{"code":0,"data":{"replyKeyboard":{"keyboard":[[{"text":"上班"}]]}}}`
		if r.URL.Host == "api.telegram.org" {
			body = `{"ok":false,"description":"Forbidden: bot was kicked"}`
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}
	failure := &atomic.Bool{}
	msg := &tgMessage{MessageID: 42, Text: "/help"}
	msg.Chat.ID = -100123
	msg.From.ID = 123
	replyHelp(config{BotToken: "test", BackendURL: "http://backend", Failure: failure}, msg)
	if !failure.Load() {
		t.Fatal("failed keyboard send was marked successful")
	}
}

func TestUserConfigReusedOnlyWithinUpdate(t *testing.T) {
	old := http.DefaultClient
	defer func() { http.DefaultClient = old }()
	calls := 0
	http.DefaultClient = &http.Client{Transport: menuTransport(func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodGet {
			calls++
		}
		body := `{"code":0,"data":{"employeeId":"employee","replyLang":"zh","replyKeyboard":{"keyboard":[[{"text":"上班"}]]}}}`
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}
	cfg := config{BackendURL: "http://backend", configResponses: make(map[string][]byte)}
	refreshRuntimeConfigForUser(&cfg, "-123", 456)
	refreshUserLangFromBackend(cfg, "-123", 456)
	if kb := replyKeyboardForUser(cfg, "-123", 456); kb == nil {
		t.Fatal("missing keyboard")
	}
	if calls != 1 {
		t.Fatalf("language and keyboard made %d config requests", calls)
	}
	replyKeyboardForUser(cfg, "-123", 789)
	replyKeyboardForUser(cfg, "-999", 456)
	next := cfg
	next.configResponses = make(map[string][]byte)
	replyKeyboardForUser(next, "-123", 456)
	if calls != 4 {
		t.Fatalf("employee configuration leaked across scope: %d", calls)
	}
	var result apiResult
	if err := backendPost(cfg, "/telegram/worker/lang", map[string]string{"lang": "en"}, &result); err != nil {
		t.Fatal(err)
	}
	replyKeyboardForUser(cfg, "-123", 456)
	if calls != 5 {
		t.Fatal("language mutation did not invalidate configuration")
	}
}
func TestFailedUserConfigIsNotCached(t *testing.T) {
	old := http.DefaultClient
	defer func() { http.DefaultClient = old }()
	calls := 0
	http.DefaultClient = &http.Client{Transport: menuTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"code":403}`)), Header: make(http.Header)}, nil
	})}
	cfg := config{BackendURL: "http://backend", configResponses: make(map[string][]byte)}
	var result apiResult
	for n := 0; n < 2; n++ {
		if err := backendGet(cfg, "/telegram/worker/config?telegramUserId=456", &result); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 2 {
		t.Fatal("failed configuration was cached")
	}
}

func TestPunchHTTPBudgetsAndCancellation(t *testing.T) {
	old := http.DefaultClient
	defer func() { http.DefaultClient = old }()
	for _, tc := range []struct {
		name   string
		budget time.Duration
		call   func(config) error
	}{
		{"read", backendReadTimeout, func(c config) error { return backendGet(c, "/telegram/worker/config", &apiResult{}) }},
		{"write", backendWriteTimeout, func(c config) error { return backendPost(c, "/telegram/worker/checkin", nil, &apiResult{}) }},
		{"telegram", telegramSendTimeout, func(c config) error { _, e := sendTelegramHTMLReplyReturnID(c, -123, "ok", 0, nil); return e }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			http.DefaultClient = &http.Client{Transport: menuTransport(func(r *http.Request) (*http.Response, error) {
				deadline, ok := r.Context().Deadline()
				if !ok || time.Until(deadline) > tc.budget || time.Until(deadline) < tc.budget-time.Second {
					t.Fatalf("incorrect request deadline: %v", deadline)
				}
				return nil, errors.New("simulated transport failure")
			})}
			cfg := config{BackendURL: "http://backend", BotToken: "test", Failure: &atomic.Bool{}}
			if tc.call(cfg) == nil || !cfg.Failure.Load() {
				t.Fatal("transport failure was not recorded")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer cancel()
			cfg.Context = ctx
			http.DefaultClient = &http.Client{Transport: menuTransport(func(r *http.Request) (*http.Response, error) {
				<-r.Context().Done()
				return nil, r.Context().Err()
			})}
			started := time.Now()
			if err := tc.call(cfg); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("got %v", err)
			}
			if time.Since(started) > time.Second {
				t.Fatal("parent deadline did not bound request")
			}
		})
	}
}

func TestActivityCommitThenSingleDelivery(t *testing.T) {
	for _, failSend := range []bool{false, true} {
		old := http.DefaultClient
		writes, sends, anchors := 0, 0, 0
		http.DefaultClient = &http.Client{Transport: menuTransport(func(r *http.Request) (*http.Response, error) {
			body := `{"code":0,"data":{}}`
			switch {
			case strings.HasSuffix(r.URL.Path, "/activity/start"):
				writes++
				body = `{"code":0,"data":{"sessionId":"session","groupReplyHtml":"打卡成功","groupReplySent":false}}`
			case r.URL.Host == "api.telegram.org":
				if writes != 1 {
					t.Fatal("delivery happened before business response")
				}
				sends++
				if failSend {
					return nil, context.DeadlineExceeded
				}
				body = `{"ok":true,"result":{"message_id":42}}`
			case strings.HasSuffix(r.URL.Path, "/activity/message-id"):
				if sends != 1 {
					t.Fatal("anchor stored before delivery")
				}
				anchors++
			default:
				body = `{"code":0,"data":{"replyKeyboard":{"keyboard":[[{"text":"上班"}]]}}}`
			}
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
		})}
		cfg := config{BackendURL: "http://backend", BotToken: "test", Failure: &atomic.Bool{}}
		replyStartActivity(cfg, tgUserRef{ID: 123}, "-123", "小厕", 7, "")
		http.DefaultClient = old
		if writes != 1 || sends != 1 {
			t.Fatalf("unexpected replay: writes=%d sends=%d", writes, sends)
		}
		if failSend && (anchors != 0 || !cfg.Failure.Load()) {
			t.Fatal("ambiguous send must require review without recording an anchor")
		}
		if !failSend && (anchors != 1 || cfg.Failure.Load()) {
			t.Fatal("successful delivery not recorded")
		}
	}
}

func TestWorkReplyRejectionDoesNotRetryOrReportSuccess(t *testing.T) {
	old := http.DefaultClient
	defer func() { http.DefaultClient = old }()
	sends := 0
	http.DefaultClient = &http.Client{Transport: menuTransport(func(r *http.Request) (*http.Response, error) {
		sends++
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"ok":false,"description":"Forbidden"}`)), Header: make(http.Header)}, nil
	})}
	cfg := config{BotToken: "test", Failure: &atomic.Bool{}}
	replyWorkHTMLInChatMarkup(cfg, "-123", "成功", 7, nil)
	if sends != 1 || !cfg.Failure.Load() {
		t.Fatal("rejected reply was retried or marked successful")
	}
}

func TestPunchTransportErrorDoesNotClaimFailedMutation(t *testing.T) {
	old := http.DefaultClient
	defer func() { http.DefaultClient = old }()
	message := ""
	http.DefaultClient = &http.Client{Transport: menuTransport(func(r *http.Request) (*http.Response, error) {
		body := `{"code":0,"data":{"replyKeyboard":{"keyboard":[[{"text":"上班"}]]}}}`
		if r.URL.Host == "api.telegram.org" {
			var payload struct {
				Text string `json:"text"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			message = payload.Text
			body = `{"ok":true,"result":{"message_id":42}}`
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}
	replyPunchTransportError(config{BackendURL: "http://backend", BotToken: "test"}, tgUserRef{ID: 7654321}, "-123", punchFailPrefix, context.DeadlineExceeded, 7)
	if !strings.Contains(message, "结果暂未确认") || strings.Contains(message, "打卡失败") {
		t.Fatalf("misleading response: %s", message)
	}
}

func TestActivityBackUsesOriginalGroupAndPreservesStartAnchor(t *testing.T) {
	old := http.DefaultClient
	defer func() { http.DefaultClient = old }()
	sent, recorded := false, false
	http.DefaultClient = &http.Client{Transport: menuTransport(func(r *http.Request) (*http.Response, error) {
		var payload map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		body := `{"code":0}`
		if r.URL.Host == "api.telegram.org" {
			sent = true
			reply, _ := payload["reply_parameters"].(map[string]interface{})
			if payload["chat_id"] != float64(-999) || reply["message_id"] != float64(42) {
				t.Fatalf("wrong destination/anchor: %#v", payload)
			}
			body = `{"ok":true,"result":{"message_id":43}}`
		} else {
			recorded = true
			if payload["ended"] != true || payload["messageId"] != float64(43) || payload["sessionId"] != "session" {
				t.Fatalf("start anchor would be overwritten: %#v", payload)
			}
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}
	cfg := config{BackendURL: "http://backend", BotToken: "test", Failure: &atomic.Bool{}}
	replyActivityEndHTML(cfg, "123", "已回座", 7, map[string]interface{}{"groupReplyChatId": "-999", "sessionId": "session", "activityAnchorMessageId": float64(42)})
	if !sent || !recorded || cfg.Failure.Load() {
		t.Fatal("activity back delivery incomplete")
	}
}

func TestFailedDeviceLookupDoesNotExecutePunch(t *testing.T) {
	old := http.DefaultClient
	defer func() { http.DefaultClient = old }()
	http.DefaultClient = &http.Client{Transport: menuTransport(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/punch-devices") {
			return nil, context.DeadlineExceeded
		}
		body := `{"code":0,"data":{"replyKeyboard":{"keyboard":[[{"text":"上班"}]]}}}`
		if r.URL.Host == "api.telegram.org" {
			body = `{"ok":true,"result":{"message_id":42}}`
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}
	executed := false
	withDeviceSelection(config{BackendURL: "http://backend", BotToken: "test"}, 123, "-123", 7, "ci", "day", "", func() { executed = true })
	if executed {
		t.Fatal("punch executed after failed device resolution")
	}
}

func TestHelpReusesCurrentUserConfiguration(t *testing.T) {
	old := http.DefaultClient
	defer func() { http.DefaultClient = old }()
	reads, sends := 0, 0
	http.DefaultClient = &http.Client{Transport: menuTransport(func(r *http.Request) (*http.Response, error) {
		body := `{"code":0,"data":{"replyKeyboard":{"keyboard":[[{"text":"上班"}]]}}}`
		if r.URL.Host == "api.telegram.org" {
			sends++
			body = `{"ok":true,"result":{"message_id":43}}`
		} else {
			reads++
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}
	cfg := config{BackendURL: "http://backend", BotToken: "test", configResponses: make(map[string][]byte)}
	refreshRuntimeConfigForUser(&cfg, "-123", 123)
	msg := &tgMessage{MessageID: 42, Text: "/help"}
	msg.Chat.ID = -123
	msg.From.ID = 123
	replyHelp(cfg, msg)
	if reads != 1 || sends != 1 {
		t.Fatalf("redundant help configuration: reads=%d sends=%d", reads, sends)
	}
}

func TestFreshEmployeeConfigSkipsOnlyDevicePicker(t *testing.T) {
	old := http.DefaultClient
	defer func() { http.DefaultClient = old }()
	lookups := 0
	http.DefaultClient = &http.Client{Transport: menuTransport(func(r *http.Request) (*http.Response, error) {
		lookups++
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"code":0,"data":{"requiresSelection":false}}`)), Header: make(http.Header)}, nil
	})}
	path := "/telegram/worker/config?chatId=-123&telegramUserId=123"
	cfg := config{BackendURL: "http://backend", configResponses: map[string][]byte{path: []byte(`{"code":0,"data":{"employeeId":"b94806b6-a1ab-4f34-8a91-39cb0192c551"}}`)}}
	executed := 0
	run := func() { withDeviceSelection(cfg, 123, "-123", 7, "ci", "day", "", func() { executed++ }) }
	run()
	if executed != 1 || lookups != 0 {
		t.Fatal("fresh identity did not skip redundant picker lookup")
	}
	cfg.configResponses = make(map[string][]byte) // A new update must not reuse that identity.
	run()
	if executed != 2 || lookups != 1 {
		t.Fatal("identity leaked into next update")
	}
	cfg.configResponses[path] = []byte(`{"code":0,"data":{"employeeId":""}}`)
	run()
	if lookups != 2 {
		t.Fatal("unresolved identity bypassed picker")
	}
}

type observedReplyBody struct {
	*strings.Reader
	closed bool
}

func (b *observedReplyBody) Close() error { b.closed = true; return nil }
func TestCallbackResponseDrainedForReuse(t *testing.T) {
	old := http.DefaultClient
	defer func() { http.DefaultClient = old }()
	body := &observedReplyBody{Reader: strings.NewReader(`{"ok":true,"result":true}`)}
	http.DefaultClient = &http.Client{Transport: menuTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: body, Header: make(http.Header)}, nil
	})}
	answerCallback(config{BotToken: "test"}, "callback", "ok")
	if !body.closed || body.Len() != 0 {
		t.Fatal("callback response prevents connection reuse")
	}
}

func TestChatDiscoveryRunsAfterReply(t *testing.T) {
	chatSeenCache.Delete(int64(-8877))
	old := http.DefaultClient
	defer func() { http.DefaultClient = old }()
	activitiesMu.Lock()
	previous := cachedButtonLookup
	cachedButtonLookup = map[string]string{"test": "test"}
	activitiesMu.Unlock()
	defer func() { activitiesMu.Lock(); cachedButtonLookup = previous; activitiesMu.Unlock() }()
	sent, seen := false, false
	http.DefaultClient = &http.Client{Transport: menuTransport(func(r *http.Request) (*http.Response, error) {
		body := `{"code":0,"data":{}}`
		if r.URL.Host == "api.telegram.org" {
			sent = true
			body = `{"ok":true,"result":{"message_id":42}}`
		}
		if strings.HasSuffix(r.URL.Path, "/chat/seen") {
			if !sent {
				t.Fatal("chat metadata delayed user reply")
			}
			seen = true
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	msg := &tgMessage{MessageID: 42, Text: "/chatid"}
	msg.Chat.ID = -8877
	msg.Chat.Type = "supergroup"
	msg.From.ID = 8877
	handleMessage(config{BackendURL: "http://backend", BotToken: "test", configResponses: make(map[string][]byte)}, tgUpdate{UpdateID: time.Now().UnixNano(), Message: msg})
	if !sent || !seen {
		t.Fatalf("reply=%v metadata=%v", sent, seen)
	}
}

func TestChatSeenWrittenOncePerRefreshWindow(t *testing.T) {
	old := http.DefaultClient
	defer func() { http.DefaultClient = old }()
	const chat = int64(-9911)
	chatSeenCache.Delete(chat)
	defer chatSeenCache.Delete(chat)
	calls := 0
	http.DefaultClient = &http.Client{Transport: menuTransport(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/chat/seen") {
			calls++
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"code":0,"data":{}}`))}, nil
	})}
	cfg := config{BackendURL: "http://backend"}
	recordChatSeen(cfg, chat, "supergroup", "Team A")
	recordChatSeen(cfg, chat, "supergroup", "Team A")
	if calls != 1 {
		t.Fatalf("unchanged group should be written once inside the window, got %d", calls)
	}
	recordChatSeen(cfg, chat, "supergroup", "Team B")
	if calls != 2 {
		t.Fatalf("a title change must be written immediately, got %d", calls)
	}
}

func TestWarmTelegramConnectionCallsGetMe(t *testing.T) {
	old := http.DefaultClient
	defer func() { http.DefaultClient = old }()
	var path string
	http.DefaultClient = &http.Client{Transport: menuTransport(func(r *http.Request) (*http.Response, error) {
		path = r.URL.Path
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"ok":true}`))}, nil
	})}
	WarmTelegramConnection("  ") // blank token: no request
	if path != "" {
		t.Fatalf("blank token must not call Telegram, got %q", path)
	}
	WarmTelegramConnection("123:abc")
	if !strings.HasSuffix(path, "/getMe") {
		t.Fatalf("expected a getMe call, got %q", path)
	}
}
