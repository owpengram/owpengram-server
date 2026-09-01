package domain

import "errors"

// 文件分片上传与媒体相关业务错误。rpc 层据此映射为对应 rpc_error（见 internal/rpc/errors.go）：
// 文件分片（part invalid/parts invalid/part too big）、上传配额、照片与文档。
var (
	ErrFilePartInvalid     = errors.New("file part invalid")
	ErrFilePartsInvalid    = errors.New("file parts invalid")
	ErrFilePartTooBig      = errors.New("file part too big")
	ErrUploadQuotaExceeded = errors.New("upload quota exceeded")
	ErrPhotoInvalid    = errors.New("photo invalid")
	ErrDocumentInvalid = errors.New("document invalid")
	// ErrStorageFull is returned when the configured low-space guard rejects a
	// write: local disk free bytes (or, on the s3 backend, the configured
	// total-bytes budget) has fallen below the configured threshold.
	ErrStorageFull = errors.New("storage full")
)
