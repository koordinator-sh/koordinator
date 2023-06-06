/*
Copyright 2013 The Cloudera Inc.
Copyright 2023 The Koordinator Authors.

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

package auth

import (
	"bytes"
	"encoding/binary"
	"log"
	"os/user"
	"runtime"
	"strings"
	"unicode"

	hadoop_common "github.com/koordinator-sh/koordinator/pkg/yarn/apis/proto/hadoopcommon"
)

var (
	RPC_HEADER        []byte = []byte("hrpc")
	VERSION           []byte = []byte{0x09}
	RPC_SERVICE_CLASS byte   = 0x00

	RPC_PROTOCOL_BUFFFER    hadoop_common.RpcKindProto                         = hadoop_common.RpcKindProto_RPC_PROTOCOL_BUFFER
	RPC_FINAL_PACKET        hadoop_common.RpcRequestHeaderProto_OperationProto = hadoop_common.RpcRequestHeaderProto_RPC_FINAL_PACKET
	RPC_DEFAULT_RETRY_COUNT int32                                              = hadoop_common.Default_RpcRequestHeaderProto_RetryCount
	CLIENT_PROTOCOL_VERSION uint64                                             = 1
)

type AuthMethod byte

const (
	AUTH_SIMPLE   AuthMethod = 0x50
	AUTH_KERBEROS AuthMethod = 0x51
	AUTH_TOKEN    AuthMethod = 0x52
	AUTH_PLAIN    AuthMethod = 0x53
)

func (authmethod AuthMethod) String() string {
	switch {
	case authmethod == AUTH_SIMPLE:
		return "SIMPLE"
	case authmethod == AUTH_KERBEROS:
		return "GSSAPI"
	case authmethod == AUTH_TOKEN:
		return "DIGEST-MD5"
	case authmethod == AUTH_PLAIN:
		return "PLAIN"
	}
	return "ERROR-UNKNOWN"
}

type AuthProtocol byte

const (
	AUTH_PROTOCOL_NONE AuthProtocol = 0x00
	AUTH_PROTOCOL_SASL AuthProtocol = 0xDF
)

func (authprotocol AuthProtocol) String() string {
	switch {
	case authprotocol == AUTH_PROTOCOL_NONE:
		return "NONE"
	case authprotocol == AUTH_PROTOCOL_SASL:
		return "SASL"
	}
	return "ERROR-UNKNOWN"
}

func ConvertFixedToBytes(data interface{}) ([]byte, error) {
	buf := new(bytes.Buffer)
	err := binary.Write(buf, binary.BigEndian, data)
	return buf.Bytes(), err
}

func ConvertBytesToFixed(rawBytes []byte, data interface{}) error {
	buf := bytes.NewBuffer(rawBytes)
	err := binary.Read(buf, binary.BigEndian, data)
	return err
}

func GetCalleeRPCRequestHeaderProto(protocolName *string) *hadoop_common.RequestHeaderProto {
	pc, _, _, _ := runtime.Caller(1) // Callee Method Name
	fullName := runtime.FuncForPC(pc).Name()
	names := strings.Split(fullName, ".")
	unicodeName := []rune(names[len(names)-1])
	unicodeName[0] = unicode.ToLower(unicodeName[0])
	methodName := string(unicodeName)
	return &hadoop_common.RequestHeaderProto{MethodName: &methodName, DeclaringClassProtocolName: protocolName, ClientProtocolVersion: &CLIENT_PROTOCOL_VERSION}
}

func CreateSimpleUGIProto() (*hadoop_common.UserInformationProto, error) {
	// Figure the current user-name
	var username string
	if user, err := user.Current(); err != nil {
		log.Fatal("user.Current", err)
		return nil, err
	} else {
		username = user.Username
	}

	return &hadoop_common.UserInformationProto{EffectiveUser: nil, RealUser: &username}, nil
}
