package handler

import (
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/learning/go-dianping/internal/middleware"
	"github.com/learning/go-dianping/internal/service"
	"github.com/learning/go-dianping/pkg/apperror"
	"github.com/learning/go-dianping/pkg/response"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var imageNamePattern = regexp.MustCompile(`^/blogs/([1-9][0-9]*)/[a-f0-9]{64}\.(png|jpg|gif|webp)$`)

func (api API) upload(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, api.Config.Upload.MaxBytes+(64<<10))
	if err := c.Request.ParseMultipartForm(1 << 20); err != nil {
		response.Fail(c, apperror.BadRequest("上传表单无效或图片过大"))
		return
	}
	defer c.Request.MultipartForm.RemoveAll()
	file, _, err := c.Request.FormFile("file")
	if err != nil {
		response.Fail(c, apperror.BadRequest("file 必填"))
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, api.Config.Upload.MaxBytes+1))
	if err != nil {
		response.Fail(c, err)
		return
	}
	if int64(len(data)) > api.Config.Upload.MaxBytes {
		response.Fail(c, apperror.BadRequest("图片超过大小限制"))
		return
	}
	ext := map[string]string{"image/png": "png", "image/jpeg": "jpg", "image/gif": "gif", "image/webp": "webp"}[http.DetectContentType(data)]
	if ext == "" {
		response.Fail(c, apperror.BadRequest("只支持 PNG/JPEG/GIF/WebP 图片"))
		return
	}
	token, err := service.RandomToken()
	if err != nil {
		response.Fail(c, err)
		return
	}
	name := fmt.Sprintf("/blogs/%d/%s.%s", middleware.UserID(c), token, ext)
	path := filepath.Join(api.Config.Upload.Directory, filepath.FromSlash(strings.TrimPrefix(name, "/")))
	if err = os.MkdirAll(filepath.Dir(path), 0750); err != nil {
		response.Fail(c, err)
		return
	}
	root, err := os.OpenRoot(api.Config.Upload.Directory)
	if err != nil {
		response.Fail(c, err)
		return
	}
	defer root.Close()
	f, err := root.OpenFile(filepath.FromSlash(strings.TrimPrefix(name, "/")), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0640)
	if err != nil {
		response.Fail(c, err)
		return
	}
	_, writeErr := f.Write(data)
	closeErr := f.Close()
	if writeErr != nil {
		_ = root.Remove(strings.TrimPrefix(name, "/"))
		response.Fail(c, writeErr)
		return
	}
	if closeErr != nil {
		response.Fail(c, closeErr)
		return
	}
	response.OK(c, name)
}
func (api API) deleteUpload(c *gin.Context) {
	name := c.Query("name")
	parts := imageNamePattern.FindStringSubmatch(name)
	if len(parts) == 0 || parts[1] != strconv.FormatInt(middleware.UserID(c), 10) {
		response.Fail(c, apperror.BadRequest("无效文件名或不属于当前用户"))
		return
	}
	root, err := os.OpenRoot(api.Config.Upload.Directory)
	if err != nil {
		response.Fail(c, err)
		return
	}
	defer root.Close()
	err = root.Remove(filepath.FromSlash(strings.TrimPrefix(name, "/")))
	if os.IsNotExist(err) {
		err = apperror.ErrNotFound
	}
	respond(c, nil, err)
}
