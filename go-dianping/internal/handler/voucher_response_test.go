package handler

import (
	"encoding/json"
	"errors"
	"github.com/gin-gonic/gin"
	"net/http/httptest"
	"testing"
)

func TestVoucherPartialSuccessKeepsIdentity(t *testing.T) {
	for _, tc := range []struct {
		id      int64
		err     error
		status  int
		success bool
	}{
		{42, errors.New("internal Redis credentials must not leak"), 503, false},
		{0, errors.New("database failed"), 500, false},
		{42, nil, 200, true},
	} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		respondVoucher(c, tc.id, tc.err)
		var body struct {
			Success  bool
			ErrorMsg string
			Data     json.RawMessage
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if w.Code != tc.status || body.Success != tc.success {
			t.Fatalf("unexpected response: %d %s", w.Code, w.Body)
		}
		if tc.status == 503 {
			var data struct {
				VoucherID int64
				State     string
			}
			if err := json.Unmarshal(body.Data, &data); err != nil {
				t.Fatal(err)
			}
			if data.VoucherID != 42 || data.State != "initialization_failed" {
				t.Fatalf("lost persisted identity: %s", body.Data)
			}
			if body.ErrorMsg == tc.err.Error() {
				t.Fatal("internal error exposed")
			}
		}
	}
}
