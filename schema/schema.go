// Package schema validates plan documents against the published JSON Schema.
package schema

import (
	"bytes"
	_ "embed"
	"fmt"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

//go:embed plan.v1.schema.json
var planV1 []byte

const planV1ID = "https://raw.githubusercontent.com/superplanehq/suss/main/schema/plan.v1.schema.json"

var (
	compileOnce sync.Once
	compiled    *jsonschema.Schema
	compileErr  error
)

func compiledPlanV1() (*jsonschema.Schema, error) {
	compileOnce.Do(func() {
		document, err := jsonschema.UnmarshalJSON(bytes.NewReader(planV1))
		if err != nil {
			compileErr = fmt.Errorf("parse plan schema: %w", err)
			return
		}

		compiler := jsonschema.NewCompiler()
		if err := compiler.AddResource(planV1ID, document); err != nil {
			compileErr = fmt.Errorf("add plan schema: %w", err)
			return
		}

		compiled, compileErr = compiler.Compile(planV1ID)
		if compileErr != nil {
			compileErr = fmt.Errorf("compile plan schema: %w", compileErr)
		}
	})
	return compiled, compileErr
}

// Validate reports whether instance is a Draft 2020-12 valid plan document.
func Validate(instance []byte) error {
	schema, err := compiledPlanV1()
	if err != nil {
		return err
	}

	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(instance))
	if err != nil {
		return fmt.Errorf("parse plan document: %w", err)
	}
	if err := schema.Validate(document); err != nil {
		return fmt.Errorf("plan document is not schema-valid: %w", err)
	}
	return nil
}
