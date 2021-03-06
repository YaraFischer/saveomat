//go:generate mockgen -package server -destination image_api_client_mock_test.go github.com/docker/docker/client ImageAPIClient

package server

import (
	"archive/tar"
	"bytes"
	"encoding/base64"
	"io"
	"io/ioutil"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/bastjan/saveomat/internal/pkg/auth"
	"github.com/docker/docker/api/types"
	"github.com/golang/mock/gomock"
	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
)

var auth64 = base64.URLEncoding.EncodeToString([]byte("test:test"))
var testAuthConf = `{
	"auths": {
		"test.io": {
			"auth": "` + auth64 + `"
		},
		"https://index.docker.io/v1/": {
			"auth": "` + auth64 + `"
		}
	}
}`

func TestBaseURL(t *testing.T) {
	var subject http.Handler

	subject = NewServer(ServerOpts{BaseURL: ""})
	expectResponseCode(t, subject, "/", http.StatusOK)
	expectResponseCode(t, subject, "/sub/", http.StatusNotFound)

	subject = NewServer(ServerOpts{BaseURL: "/sub"})
	expectResponseCode(t, subject, "/", http.StatusNotFound)
	expectResponseCode(t, subject, "/sub/", http.StatusOK)
}

func expectResponseCode(t *testing.T, handler http.Handler, path string, code int) {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, code, rec.Code)
}

func TestPostTar(t *testing.T) {
	images := []string{"busybox", "open.io/busybox", "test.io/busybox"}

	subject := NewServer(ServerOpts{
		DockerClient: dockerMockFor(t, images, nil),
	})

	upload := new(bytes.Buffer)
	mpw := multipart.NewWriter(upload)
	fw, err := mpw.CreateFormFile("images.txt", "images.txt")
	assert.NoError(t, err)
	fw.Write([]byte(strings.Join(images, "\n")))
	mpw.Close()

	req := httptest.NewRequest(http.MethodPost, "/tar", upload)
	req.Header.Set(echo.HeaderContentType, mpw.FormDataContentType())
	rec := httptest.NewRecorder()

	subject.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)

	responseTar, err := ioutil.ReadAll(rec.Body)
	assert.NoError(t, err)
	expected := mockTarBytes(t)
	assert.Equal(t, expected, responseTar)
}

func TestPostTarWithAuth(t *testing.T) {
	images := []string{"busybox", "open.io/busybox", "test.io/busybox"}

	authn, err := auth.FromReader(strings.NewReader(testAuthConf))
	assert.NoError(t, err)

	subject := NewServer(ServerOpts{
		DockerClient: dockerMockFor(t, images, authn),
	})

	upload := new(bytes.Buffer)
	mpw := multipart.NewWriter(upload)
	fw, err := mpw.CreateFormFile("images.txt", "images.txt")
	assert.NoError(t, err)
	fw.Write([]byte(strings.Join(images, "\n")))
	fw, err = mpw.CreateFormFile("config.json", "config.json")
	assert.NoError(t, err)
	fw.Write([]byte(testAuthConf))
	mpw.Close()

	req := httptest.NewRequest(http.MethodPost, "/tar", upload)
	req.Header.Set(echo.HeaderContentType, mpw.FormDataContentType())
	rec := httptest.NewRecorder()

	subject.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)

	responseTar, err := ioutil.ReadAll(rec.Body)
	assert.NoError(t, err)
	expected := mockTarBytes(t)
	assert.Equal(t, expected, responseTar)
}

func TestGetTar(t *testing.T) {
	images := []string{"busybox", "open.io/busybox"}

	subject := NewServer(ServerOpts{
		DockerClient: dockerMockFor(t, images, nil),
	})

	params := url.Values{"image": images}.Encode()
	req := httptest.NewRequest(http.MethodGet, "/tar?"+params, nil)
	rec := httptest.NewRecorder()

	subject.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)

	responseTar, err := ioutil.ReadAll(rec.Body)
	assert.NoError(t, err)
	expected := mockTarBytes(t)
	assert.Equal(t, expected, responseTar)
}

func dockerMockFor(t *testing.T, images []string, authn auth.Authenticator) *MockImageAPIClient {
	ctrl := gomock.NewController(t)
	mc := NewMockImageAPIClient(ctrl)

	if authn == nil {
		authn = auth.EmptyAuthenticator
	}

	for _, img := range images {
		emptyAuth, err := auth.RegistryAuthFor(authn, img)
		assert.NoError(t, err)
		mc.
			EXPECT().
			ImagePull(
				gomock.Any(),
				gomock.Eq(img),
				gomock.Eq(types.ImagePullOptions{
					RegistryAuth: emptyAuth,
				})).
			Return(mockProgessReader(), nil)
	}

	mc.
		EXPECT().
		ImageSave(gomock.Any(), gomock.Eq(images)).
		Return(mockTarReader(t), nil)

	return mc
}

func mockProgessReader() io.ReadCloser {
	return ioutil.NopCloser(strings.NewReader(`{}`))
}

func mockTarReader(t *testing.T) io.ReadCloser {
	t.Helper()

	content := "test tar"

	b := new(bytes.Buffer)
	tw := tar.NewWriter(b)
	tw.WriteHeader(&tar.Header{
		Name: "images",
		Size: int64(len(content)),
	})
	_, err := tw.Write([]byte(content))
	assert.NoError(t, err)
	assert.NoError(t, tw.Close())

	return ioutil.NopCloser(b)
}

func mockTarBytes(t *testing.T) []byte {
	t.Helper()
	b, err := ioutil.ReadAll(mockTarReader(t))
	assert.NoError(t, err)

	return b
}
