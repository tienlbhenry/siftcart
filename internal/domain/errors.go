package domain

import "errors"

var (
	ErrSchemaNotFound   = errors.New("schema not found")
	ErrInvalidSchema    = errors.New("invalid schema")
	ErrDocumentNotFound = errors.New("document not found")
	ErrInvalidDocument  = errors.New("invalid document")
)
