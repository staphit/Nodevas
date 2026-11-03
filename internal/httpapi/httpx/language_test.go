package httpx

import (
	"net/http/httptest"
	"testing"

	"nodevas/internal/locale"
)

// A plain link (a download) cannot set Accept-Language, so the app names its
// language in the query; that wins over the browser's own list.
func TestLanguageReadsTheQueryBeforeTheHeader(t *testing.T) {
	for _, tc := range []struct {
		target, header string
		want           locale.Language
	}{
		{"/api/projects/export?language=en", "zh-TW,zh;q=0.9", locale.English},
		{"/api/projects/export?language=zh-TW", "en-US,en;q=0.9", locale.Chinese},
		{"/api/projects/export", "zh-TW,zh;q=0.9", locale.Chinese},
		{"/api/projects/export?language=", "zh-TW,zh;q=0.9", locale.Chinese},
		{"/api/projects/export", "", locale.English},
	} {
		request := httptest.NewRequest("GET", tc.target, nil)
		if tc.header != "" {
			request.Header.Set("Accept-Language", tc.header)
		}
		if got := Language(request); got != tc.want {
			t.Errorf("Language(%s, Accept-Language %q) = %s, want %s", tc.target, tc.header, got, tc.want)
		}
	}
}
