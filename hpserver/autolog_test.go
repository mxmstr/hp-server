package hpserver

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAutologHandlerServesBootstrapDocuments(t *testing.T) {
	handler := AutologHandler(nil)

	manifestReq := httptest.NewRequest(http.MethodGet, "/al/pc-052.manifest", nil)
	manifestRes := httptest.NewRecorder()
	handler.ServeHTTP(manifestRes, manifestReq)
	if manifestRes.Code != http.StatusOK || !strings.HasPrefix(manifestRes.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("manifest: status=%d content-type=%q", manifestRes.Code, manifestRes.Header().Get("Content-Type"))
	}
	var manifest struct {
		ServerActive bool `json:"serverActive"`
		Allow        struct {
			AccessOnline bool `json:"accessOnline"`
			PostPhotos   bool `json:"postPhotos"`
		} `json:"allow"`
	}
	if err := json.NewDecoder(manifestRes.Body).Decode(&manifest); err != nil {
		t.Fatalf("manifest JSON: %v", err)
	}
	if !manifest.ServerActive || !manifest.Allow.AccessOnline || manifest.Allow.PostPhotos {
		t.Fatalf("manifest=%+v", manifest)
	}

	tests := []struct {
		path        string
		contentType string
		wantBody    string
	}{
		{"/_services/json_portal.ws.php", "application/json", `{}`},
	}
	for _, tc := range tests {
		req := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader("request"))
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if res.Code != http.StatusOK || !strings.HasPrefix(res.Header().Get("Content-Type"), tc.contentType) {
			t.Fatalf("%s: status=%d content-type=%q", tc.path, res.Code, res.Header().Get("Content-Type"))
		}
		body, err := io.ReadAll(res.Result().Body)
		if err != nil {
			t.Fatal(err)
		}
		if tc.wantBody != "" && !strings.Contains(string(body), tc.wantBody) {
			t.Fatalf("%s: body=%q", tc.path, body)
		}
	}
}

func TestAutologHandlerServesConnectedOnlineMenu(t *testing.T) {
	handler := AutologHandler(nil)
	req := httptest.NewRequest(http.MethodGet,
		"/_services/json_portal.ws.php?session=LOCAL-WAL-SESSION-KEY&platform=PC&language=en_US&version=052&pName=Player&getComponent=online_menu",
		nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)

	if res.Code != http.StatusOK || !strings.HasPrefix(res.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("online_menu: status=%d content-type=%q", res.Code, res.Header().Get("Content-Type"))
	}

	var menu struct {
		Items []struct {
			Headline string `json:"Headline"`
			Subtitle string `json:"Subtitle"`
			Action   string `json:"Action"`
		} `json:"Items"`
		Icons []string `json:"Icons"`
	}
	if err := json.NewDecoder(res.Body).Decode(&menu); err != nil {
		t.Fatalf("online_menu JSON: %v", err)
	}

	wantActions := []string{"join", "find", "create"}
	wantIcons := []string{"!pal:join_friends", "!pal:quick_match", "!pal:create_game"}
	if len(menu.Items) != len(wantActions) || len(menu.Icons) != len(menu.Items) {
		t.Fatalf("online_menu: items=%d icons=%d", len(menu.Items), len(menu.Icons))
	}
	for i, wantAction := range wantActions {
		if menu.Items[i].Headline == "" || menu.Items[i].Action != wantAction {
			t.Fatalf("online_menu item %d: %+v", i, menu.Items[i])
		}
		if menu.Icons[i] != wantIcons[i] {
			t.Fatalf("online_menu icon %d: got %q want %q", i, menu.Icons[i], wantIcons[i])
		}
	}
}
