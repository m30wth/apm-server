// Licensed to Elasticsearch B.V. under one or more contributor
// license agreements. See the NOTICE file distributed with
// this work for additional information regarding copyright
// ownership. Elasticsearch B.V. licenses this file to you under
// the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing,
// software distributed under the License is distributed on an
// "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
// KIND, either express or implied.  See the License for the
// specific language governing permissions and limitations
// under the License.

package modeldecoder

import (
	"github.com/pkg/errors"
	"github.com/santhosh-tekuri/jsonschema"

	m "github.com/elastic/apm-server/model"
	modelerror "github.com/elastic/apm-server/model/error"
	"github.com/elastic/apm-server/model/error/generated/schema"
	"github.com/elastic/apm-server/model/field"
	"github.com/elastic/apm-server/transform"
	"github.com/elastic/apm-server/utility"
	"github.com/elastic/apm-server/validation"
)

var (
	errorSchema      = validation.CreateSchema(schema.ModelSchema, "error")
	rumV3ErrorSchema = validation.CreateSchema(schema.RUMV3Schema, "error")
)

// DecodeRUMV3Error decodes a v3 RUM error.
func DecodeRUMV3Error(input Input) (transform.Transformable, error) {
	return decodeError(input, rumV3ErrorSchema)
}

// DecodeError decodes a v2 error.
func DecodeError(input Input) (transform.Transformable, error) {
	return decodeError(input, errorSchema)
}

func decodeError(input Input, schema *jsonschema.Schema) (transform.Transformable, error) {
	raw, err := validation.ValidateObject(input.Raw, schema)
	if err != nil {
		return nil, errors.Wrap(err, "failed to validate error")
	}

	fieldName := field.Mapper(input.Config.HasShortFieldNames)
	ctx, err := decodeContext(getObject(raw, fieldName("context")), input.Config, &input.Metadata)
	if err != nil {
		return nil, err
	}

	decoder := utility.ManualDecoder{}
	e := modelerror.Event{
		Metadata:           input.Metadata,
		Id:                 decoder.StringPtr(raw, "id"),
		Culprit:            decoder.StringPtr(raw, fieldName("culprit")),
		Labels:             ctx.Labels,
		Page:               ctx.Page,
		Http:               ctx.Http,
		Url:                ctx.Url,
		Custom:             ctx.Custom,
		Experimental:       ctx.Experimental,
		Client:             ctx.Client,
		Timestamp:          decoder.TimeEpochMicro(raw, "timestamp"),
		TransactionId:      decoder.StringPtr(raw, "transaction_id"),
		ParentId:           decoder.StringPtr(raw, "parent_id"),
		TraceId:            decoder.StringPtr(raw, "trace_id"),
		TransactionSampled: decoder.BoolPtr(raw, fieldName("sampled"), fieldName("transaction")),
		TransactionType:    decoder.StringPtr(raw, fieldName("type"), fieldName("transaction")),
	}

	ex := decoder.MapStr(raw, fieldName("exception"))
	e.Exception = decodeException(&decoder, input.Config.HasShortFieldNames)(ex)

	log := decoder.MapStr(raw, fieldName("log"))
	logMsg := decoder.StringPtr(log, fieldName("message"))
	if logMsg != nil {
		e.Log = &modelerror.Log{
			Message:      *logMsg,
			ParamMessage: decoder.StringPtr(log, fieldName("param_message")),
			Level:        decoder.StringPtr(log, fieldName("level")),
			LoggerName:   decoder.StringPtr(log, fieldName("logger_name")),
			Stacktrace:   m.Stacktrace{},
		}
		var stacktrace *m.Stacktrace
		stacktrace, decoder.Err = decodeStacktrace(log[fieldName("stacktrace")], input.Config.HasShortFieldNames, decoder.Err)
		if stacktrace != nil {
			e.Log.Stacktrace = *stacktrace
		}
	}
	if decoder.Err != nil {
		return nil, decoder.Err
	}
	if e.Timestamp.IsZero() {
		e.Timestamp = input.RequestTime
	}

	return &e, nil
}

type exceptionDecoder func(map[string]interface{}) *modelerror.Exception

func decodeException(decoder *utility.ManualDecoder, hasShortFieldNames bool) exceptionDecoder {
	var decode exceptionDecoder
	fieldName := field.Mapper(hasShortFieldNames)
	decode = func(exceptionTree map[string]interface{}) *modelerror.Exception {
		exMsg := decoder.StringPtr(exceptionTree, fieldName("message"))
		exType := decoder.StringPtr(exceptionTree, fieldName("type"))
		if decoder.Err != nil || (exMsg == nil && exType == nil) {
			return nil
		}
		ex := modelerror.Exception{
			Message:    exMsg,
			Type:       exType,
			Code:       decoder.Interface(exceptionTree, fieldName("code")),
			Module:     decoder.StringPtr(exceptionTree, fieldName("module")),
			Attributes: decoder.Interface(exceptionTree, fieldName("attributes")),
			Handled:    decoder.BoolPtr(exceptionTree, fieldName("handled")),
			Stacktrace: m.Stacktrace{},
		}
		var stacktrace *m.Stacktrace
		stacktrace, decoder.Err = decodeStacktrace(exceptionTree[fieldName("stacktrace")], hasShortFieldNames, decoder.Err)
		if stacktrace != nil {
			ex.Stacktrace = *stacktrace
		}
		for _, cause := range decoder.InterfaceArr(exceptionTree, fieldName("cause")) {
			e, ok := cause.(map[string]interface{})
			if !ok {
				decoder.Err = errors.New("cause must be an exception")
				return nil
			}
			nested := decode(e)
			if nested != nil {
				ex.Cause = append(ex.Cause, *nested)
			}
		}
		return &ex
	}
	return decode
}
