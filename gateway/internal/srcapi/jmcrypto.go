package srcapi

import (
	"bytes"
	"crypto/aes"
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"fmt"
)

// JM app constants, ported from app/lib/network/jm_network/{headers,jm_network}.dart.
const (
	// jmAuthKey seeds the request token: md5("<time><jmAuthKey>").
	jmAuthKey = "18comicAPPContent"
	// jmDataSecret is appended to the request time to derive the response key.
	jmDataSecret = "185Hcomic3PAPP7R"
	// jmAppVersion rides along in tokenparam.
	jmAppVersion = "2.0.11"
	// JMUserAgent mirrors the Android webview the official app ships.
	JMUserAgent = "Mozilla/5.0 (Linux; Android 10; K; wv) AppleWebKit/537.36 (KHTML, like Gecko) Version/4.0 Chrome/138.0.0.0 Mobile Safari/537.36"
	// jmDefaultScramble is what the Dart client hardcodes for every chapter;
	// current /chapter responses no longer carry scramble_id.
	jmDefaultScramble = "220980"
)

// jmAPIDomainsSeed are the factory-default API lines (settings[85]). They are
// periodically retired upstream; the live list comes from jmDomainURLs.
var jmAPIDomainsSeed = []string{
	"www.cdntwice.org",
	"www.cdnsha.org",
	"www.cdnaspa.cc",
	"www.cdnntr.cc",
}

// jmDomainURLs host the encrypted live API domain list.
var jmDomainURLs = []string{
	"https://rup4a04-c02.tos-cn-hongkong.bytepluses.com/newsvr-2025.txt",
	"https://rup4a04-c01.tos-ap-southeast-1.bytepluses.com/newsvr-2025.txt",
}

// jmDomainSecret decrypts the server list (char codes from jm_network.dart).
var jmDomainSecret = string([]byte{
	100, 105, 111, 115, 102, 106, 99, 107, 119, 112, 113, 112,
	100, 102, 106, 107, 118, 110, 113, 81, 106, 115, 105, 107,
})

// jmImgBaseSeed is the factory-default image CDN (settings[86]).
const jmImgBaseSeed = "https://cdn-msp.jmapiproxy3.cc"

// jmToken returns the request "token" header value for a unix time.
func jmToken(timeSec int64) string {
	return jmMD5Hex(fmt.Sprintf("%d%s", timeSec, jmAuthKey))
}

// jmTokenParam returns the request "tokenparam" header value.
func jmTokenParam(timeSec int64) string {
	return fmt.Sprintf("%d,%s", timeSec, jmAppVersion)
}

func jmMD5Hex(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

// jmDecrypt decrypts the AES-256-ECB payload used by the JM API. The key is
// the hex string of md5(secret), used as raw UTF-8 bytes (32 bytes = AES-256).
// Ported from JmNetwork.convertData: base64 decode, ECB decrypt, then
// truncate everything after the last '}' or ']'.
func jmDecrypt(input, secret string) (string, error) {
	keyHex := jmMD5Hex(secret)
	block, err := aes.NewCipher([]byte(keyHex))
	if err != nil {
		return "", fmt.Errorf("srcapi/jm: cipher: %w", err)
	}
	data, err := base64.StdEncoding.DecodeString(input)
	if err != nil {
		return "", fmt.Errorf("srcapi/jm: base64: %w", err)
	}
	if len(data) == 0 || len(data)%block.BlockSize() != 0 {
		return "", fmt.Errorf("srcapi/jm: encrypted payload length %d is not a multiple of %d", len(data), block.BlockSize())
	}
	plain := make([]byte, len(data))
	for off := 0; off < len(data); off += block.BlockSize() {
		block.Decrypt(plain[off:off+block.BlockSize()], data[off:off+block.BlockSize()])
	}
	s := string(plain)
	end := -1
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '}' || s[i] == ']' {
			end = i
			break
		}
	}
	if end < 0 {
		return "", fmt.Errorf("srcapi/jm: decrypted payload has no JSON terminator")
	}
	return s[:end+1], nil
}

// jmEncrypt is the inverse of jmDecrypt (test helper, mirrors the upstream
// padding so round-trips exercise the exact truncation behavior).
func jmEncrypt(plain, secret string) (string, error) {
	keyHex := jmMD5Hex(secret)
	block, err := aes.NewCipher([]byte(keyHex))
	if err != nil {
		return "", err
	}
	data := pkcs7Pad([]byte(plain), block.BlockSize())
	out := make([]byte, len(data))
	for off := 0; off < len(data); off += block.BlockSize() {
		block.Encrypt(out[off:off+block.BlockSize()], data[off:off+block.BlockSize()])
	}
	return base64.StdEncoding.EncodeToString(out), nil
}

func pkcs7Pad(data []byte, size int) []byte {
	pad := size - len(data)%size
	return append(append([]byte{}, data...), bytes.Repeat([]byte{byte(pad)}, pad)...)
}
