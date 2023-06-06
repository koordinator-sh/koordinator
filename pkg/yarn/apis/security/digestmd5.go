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

package security

import (
	"crypto/md5"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"log"
	"strings"

	hadoop_common "github.com/koordinator-sh/koordinator/pkg/yarn/apis/proto/hadoopcommon"
)

func getChallengeParams(challenge string) (map[string]string, error) {
	challengeParams := make(map[string]string)
	splits := strings.Split(string(challenge), ",")

	for _, split := range splits {
		//split on first '='
		keyVal := strings.SplitN(split, "=", 2)

		if len(keyVal) != 2 {
			log.Fatal("found invalid param: ", split)
			return nil, errors.New("found invalid param: " + split)
		}

		key := keyVal[0]
		value := keyVal[1]

		//some challenge params are quoted (realm, nonce, qop).
		//strip these out.
		quote := "\""
		if strings.HasPrefix(value, quote) && strings.HasSuffix(value, quote) {
			value = value[len(quote):]
			value = value[:len(value)-len(quote)]
		}

		challengeParams[key] = value
	}

	return challengeParams, nil
}

//we only support a very specific digest-md5 mechanism for the moment
//multiple realm, qop not supported
func validateChallengeParameters(challengeParams map[string]string) error {
	var errString string

	realm, exists := challengeParams["realm"]
	if !exists || len(realm) == 0 {
		errString += "missing or invalid realm. "
	}

	nonce, exists := challengeParams["nonce"]
	if !exists || len(nonce) == 0 {
		errString += "missing or invalid nonce. "
	}

	qop, exists := challengeParams["qop"]
	if !exists || qop != "auth" {
		errString += "missing, invalid or unsupported qop. "
	}

	charset, exists := challengeParams["charset"]
	if !exists || charset != "utf-8" {
		errString += "missing, invalid or unsupported charset. "
	}

	algorithm, exists := challengeParams["algorithm"]
	if !exists || algorithm != "md5-sess" {
		errString += "missing, invalid or unsupported algorithm. "
	}

	if len(errString) > 0 {
		return errors.New(errString)
	}

	return nil
}

func generateChallengeReponse(username string, password string, protocol string, serverId string, challengeParams map[string]string) (string, error) {
	buffer := make([]string, 0, 128)

	charset := "charset=utf-8"
	quote := "\""
	comma := ","
	maxbuf := "maxbuf=65536"
	nonceCount := "nc=00000001"
	nonceCountHex := "00000001"

	realm := challengeParams["realm"]
	nonce := challengeParams["nonce"]
	qop := challengeParams["qop"]
	digestUri := protocol + "/" + serverId
	method := "AUTHENTICATE"

	buffer = append(buffer, charset, comma)
	buffer = append(buffer, "username=", quote, username, quote, comma)
	buffer = append(buffer, "realm=", quote, realm, quote, comma)
	buffer = append(buffer, "nonce=", quote, nonce, quote, comma)
	buffer = append(buffer, nonceCount, comma) //nonce count is one

	//generate a response nonce
	count := 30
	nonceBuffer := make([]byte, count)
	rand.Read(nonceBuffer)
	encodedNonce := base64.StdEncoding.EncodeToString(nonceBuffer)
	buffer = append(buffer, "cnonce=", quote, encodedNonce, quote, comma)

	buffer = append(buffer, "digest-uri=", quote, digestUri, quote, comma)
	buffer = append(buffer, maxbuf, comma)

	//for the md5-sess/qop=auth case, the computation is :
	//HA1=MD5(MD5(username:realm:password):nonce:cnonce)
	//HA2=MD5(method:digestURI)//
	//response=MD5(HA1:nonce:nonceCount:clientNonce:qop:HA2)

	hashSize := 16 //16 bytes
	ha1Part1 := username + ":" + realm + ":" + password
	ha1Part2 := ":" + nonce + ":" + encodedNonce
	ha1Part1md5 := md5.Sum([]byte(ha1Part1))
	ha1Input := string(ha1Part1md5[:hashSize]) + ha1Part2

	HA1 := md5.Sum([]byte(ha1Input))
	HA2 := md5.Sum([]byte(method + ":" + digestUri))

	ha1Hex := hex.EncodeToString(HA1[:hashSize])
	ha2Hex := hex.EncodeToString(HA2[:hashSize])

	responseHashInput := string(ha1Hex + ":" + nonce + ":" + nonceCountHex + ":" + encodedNonce + ":" + qop + ":" + ha2Hex)
	responseHash := md5.Sum([]byte(responseHashInput))
	responseHashHex := hex.EncodeToString(responseHash[:hashSize])
	//end digest-md5 computation

	buffer = append(buffer, "response=", responseHashHex, comma)
	buffer = append(buffer, "qop=", qop)
	response := strings.Join(buffer, "")

	log.Printf("generated challenge response: %s", response)

	return response, nil
}

func GetDigestMD5ChallengeResponse(protocol string, serverId string, challenge []byte, userToken *hadoop_common.TokenProto) (string, error) {
	if len(challenge) <= 0 {
		log.Fatal("challenge cannot be empty!")

		return "", errors.New("challenge cannot be empty!")
	}

	var err error

	challengeParams, err := getChallengeParams(string(challenge))
	if err != nil {
		log.Fatal("challenge params extraction failure! ", err)
		return "", err
	}

	err = validateChallengeParameters(challengeParams)
	if err != nil {
		log.Fatal("challenge params validation failure! ", err)
		return "", err
	}

	username := base64.StdEncoding.EncodeToString(userToken.GetIdentifier())
	password := base64.StdEncoding.EncodeToString(userToken.GetPassword())
	response, err := generateChallengeReponse(username, password, protocol, serverId, challengeParams)

	if err != nil {
		log.Fatal("Failed to generate challenge response! ", err)
		return "", err
	}

	return response, nil
}
