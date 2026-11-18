package domain

import "errors"

var (
	ErrSchemaNotFound   = errors.New("schema not found")
	ErrInvalidSchema    = errors.New("invalid schema")
	ErrDocumentNotFound = errors.New("document not found")
	ErrInvalidDocument  = errors.New("invalid document")
	ErrInvalidAIRequest = errors.New("invalid ai request")
	ErrAIRateLimited    = errors.New("ai provider rate limited")
	ErrAIProviderFailed = errors.New("ai provider failed")
)
