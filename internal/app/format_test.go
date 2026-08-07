package app

import (
	"context"
	"errors"
	"testing"

	"github.com/PierpaoloPernici/lazycaddy/internal/validator"
)

func TestFormatterFunc_PassesThrough(t *testing.T) {
	var gotSrc []byte
	fn := FormatterFunc(func(ctx context.Context, src []byte) ([]byte, []validator.Diagnostic, error) {
		gotSrc = src
		return []byte("formatted"), []validator.Diagnostic{
			{Path: "p", Line: 1, Column: 2, Message: "msg", Severity: validator.SeverityError},
		}, nil
	})
	formatted, diags, err := fn.FormatAndValidate(context.Background(), []byte("raw"))
	if err != nil {
		t.Fatalf("FormatAndValidate: unexpected error: %v", err)
	}
	if string(gotSrc) != "raw" {
		t.Errorf("src = %q, want raw", gotSrc)
	}
	if string(formatted) != "formatted" {
		t.Errorf("formatted = %q, want formatted", formatted)
	}
	if len(diags) != 1 {
		t.Fatalf("diags len = %d, want 1", len(diags))
	}
	if diags[0].Line != 1 || diags[0].Column != 2 || diags[0].Message != "msg" {
		t.Errorf("diag = %+v, want line 1 col 2 msg", diags[0])
	}
}

func TestFormatterFunc_PropagatesError(t *testing.T) {
	sentinel := errors.New("boom")
	fn := FormatterFunc(func(ctx context.Context, src []byte) ([]byte, []validator.Diagnostic, error) {
		return nil, nil, sentinel
	})
	_, _, err := fn.FormatAndValidate(context.Background(), nil)
	if !errors.Is(err, sentinel) {
		t.Errorf("err = %v, want %v", err, sentinel)
	}
}

func TestNewFormatter_WrapsValidator(t *testing.T) {
	// Construct a validator with a non-existent binary. The wrapper
	// must propagate the ErrBinaryMissing sentinel without swallowing
	// it, so the UI can render an actionable message.
	v, err := validator.New(validator.Options{BinaryPath: "/no/such/binary-1234"})
	if err != nil {
		t.Fatalf("validator.New: %v", err)
	}
	formatter := NewFormatter(v)
	_, _, err = formatter.FormatAndValidate(context.Background(), []byte("raw"))
	if !errors.Is(err, validator.ErrBinaryMissing) {
		t.Fatalf("expected ErrBinaryMissing, got %v", err)
	}
}
