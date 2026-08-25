package httpui

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"stage-rigging-clearance/internal/application"
)

func decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	if !strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
		writeAPIError(w, 415, "content_type", "请求必须使用 application/json")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		if strings.Contains(err.Error(), "request body too large") {
			writeAPIError(w, 413, "body_too_large", "请求体不得超过 1 MiB")
		} else {
			writeAPIError(w, 400, "invalid_json", "JSON 格式或字段无效："+err.Error())
		}
		return false
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		writeAPIError(w, 400, "invalid_json", "请求体只能包含一个 JSON 对象")
		return false
	}
	return true
}

func pagination(r *http.Request) (int, int) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if limit < 1 || limit > 100 {
		limit = 30
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

func writeError(w http.ResponseWriter, err error) {
	mapped := application.MapError(err)
	writeAPIError(w, mapped.Status, mapped.Code, mapped.Message)
}

func writeAPIError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(value)
}
