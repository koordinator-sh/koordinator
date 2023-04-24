/*
Copyright 2022 The Koordinator Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/
package sloconfig

import (
	"sync"

	"github.com/go-playground/locales/en"
	"github.com/go-playground/locales/zh"
	ut "github.com/go-playground/universal-translator"
	"github.com/go-playground/validator/v10"
	transen "github.com/go-playground/validator/v10/translations/en"
	transzh "github.com/go-playground/validator/v10/translations/zh"
)

var validatorInstance = &DefaultValidator{}

type DefaultValidator struct {
	once      sync.Once
	validator *validator.Validate
	trans     *ut.Translator
}

// StructWithTrans support en or zh translator
func (v *DefaultValidator) StructWithTrans(config interface{}) (validator.ValidationErrorsTranslations, error) {
	err := v.validator.Struct(config)
	switch err.(type) {
	case validator.ValidationErrors:
		if v.trans != nil {
			return err.(validator.ValidationErrors).Translate(*v.trans), nil
		}
	default:
	}
	return nil, err
}

func GetValidatorInstance() *DefaultValidator {
	validatorInstance.once.Do(func() {
		validatorInstance.validator, validatorInstance.trans = createValidator()
	})
	return validatorInstance
}

func createValidator() (*validator.Validate, *ut.Translator) {
	instance := validator.New()
	switch DefaultTranslator {
	case Zh:
		return instance, registerZhTranslator(instance)
	case En:
		return instance, registerEnTranslator(instance)
	default:
		return instance, registerEnTranslator(instance)
	}
}

func registerEnTranslator(instance *validator.Validate) *ut.Translator {
	locale := en.New()
	uni := ut.New(locale, locale)
	trans, _ := uni.GetTranslator(En)
	err := transen.RegisterDefaultTranslations(instance, trans)
	if err != nil {
		return nil
	}
	return &trans
}

func registerZhTranslator(instance *validator.Validate) *ut.Translator {
	locale := zh.New()
	uni := ut.New(locale, locale)
	trans, _ := uni.GetTranslator(Zh)
	err := transzh.RegisterDefaultTranslations(instance, trans)
	if err != nil {
		return nil
	}
	return &trans
}
