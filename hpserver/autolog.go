package hpserver

import (
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
)

func AutologHandler(logger *slog.Logger) http.Handler {

	if logger == nil {
		logger = slog.Default()
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {

		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			logger.Warn("Autolog request body read failed", "error", err)
			http.Error(w, "", http.StatusBadRequest)
			return
		}

		logger.Info("Autolog request",
			"remote", r.RemoteAddr,
			"method", r.Method,
			"host", r.Host,
			"path", r.URL.Path,
			"query", r.URL.RawQuery,
			"content_type", r.Header.Get("Content-Type"),
			"user_agent", r.UserAgent(),
			"body_bytes", len(body),
			"body_hex", hex.EncodeToString(body))
		w.Header().Set("Cache-Control", "no-store")

		switch {
		case strings.HasSuffix(strings.ToLower(r.URL.Path), ".manifest"):

			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"serverActive": true,
				"allow": map[string]interface{}{
					"accessOnline": true,
					"postPhotos":   false,
				},
			})

		case strings.HasSuffix(strings.ToLower(r.URL.Path), ".php"),
			strings.Contains(strings.ToLower(r.URL.Path), "json"):

			w.Header().Set("Content-Type", "application/json")

			// Parse URL queries
			component := r.URL.Query().Get("getComponent")

			responseData := map[string]interface{}{}

			switch component {
			case "main_menu":
				responseData = map[string]interface{}{
					"GotoSettings": "SETTINGS", // Changed to string
					"Items": []map[string]interface{}{
						// Changed all 'Title' values to literal strings
						{"Name": "photos", "Title": "PHOTOS", "Link": "photos", "NeedsConnection": 1, "Icon": "PalPhotos", "Text1": "", "Text2": "", "Text3": ""},
						{"Name": "wall", "Title": "WALL", "Link": "wall", "NeedsConnection": 1, "Icon": "PalWall", "Text1": "", "Text2": "", "Text3": ""},
						{"Name": "Online", "Title": "ONLINE", "Link": "online", "NeedsConnection": 1, "Icon": "PalFriends", "Text1": "", "Text2": "", "Text3": ""},
						{"Name": "Career", "Title": "CAREER", "Link": "event_map", "NeedsConnection": 0, "Icon": "PalCareer", "Text1": "", "Text2": "", "Text3": ""},
						{"Name": "AutologRecommends", "Title": "AUTOLOG RECOMMENDS", "Link": "al_recommends", "NeedsConnection": 1, "Icon": "PalRecommends", "Text1": "", "Text2": "", "Text3": ""},
						{"Name": "News", "Title": "NEWS", "Link": "news", "NeedsConnection": 1, "Icon": "PalNews", "Text1": "", "Text2": "", "Text3": ""},
						{"Name": "Settings", "Title": "SETTINGS", "Link": "settings", "NeedsConnection": 0, "Icon": "PalSettings", "Text1": "", "Text2": "", "Text3": ""},
					},
				}
			case "autolog_recommends":
				responseData = map[string]interface{}{
					// Provide an empty array so it doesn't crash trying to render recommends
					"Recommends": []interface{}{},
				}
			case "news_stories":
				responseData = map[string]interface{}{
					"NumStories": 0,
				}
			case "online_menu":
				responseData = map[string]interface{}{
					"Items": []map[string]interface{}{
						{"Headline": "JOIN FRIENDS", "Subtitle": "", "Action": "join"},
						{"Headline": "QUICK MATCH", "Subtitle": "", "Action": "find"},
						{"Headline": "CREATE GAME", "Subtitle": "", "Action": "create"},
					},
					"Icons": []string{
						"!pal:join_friends",
						"!pal:quick_match",
						"!pal:create_game",
					},
				}
			}

			_ = json.NewEncoder(w).Encode(responseData)
			// _ = json.NewEncoder(w).Encode(map[string]interface{}{
			// 	"error":  0,
			// 	"status": "ok",
			// 	"data":   responseData,
			// })

		default:
			w.Header().Set("Content-Type", "application/octet-stream")
			w.WriteHeader(http.StatusOK)
		}

	})

}
