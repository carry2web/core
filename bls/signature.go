//go:build relic

package bls

import (
	"bytes"
	"encoding/hex"
	"errors"
	flowCrypto "github.com/onflow/flow-go/crypto"
	"strings"
)

const SigningAlgorithm = flowCrypto.BLSBLS12381

// TODO: what should the domainTag param be?
var HashingAlgorithm = flowCrypto.NewExpandMsgXOFKMAC128("deso-protocol")

func AggregateSignatures(signatures []*Signature) (*Signature, error) {
	var flowSignatures []flowCrypto.Signature
	for _, signature := range signatures {
		flowSignatures = append(flowSignatures, signature.flowSignature)
	}
	aggregateFlowSignature, err := flowCrypto.AggregateBLSSignatures(flowSignatures)
	if err != nil {
		return nil, err
	}
	return &Signature{flowSignature: aggregateFlowSignature}, nil
}

func VerifyAggregateSignature(publicKeys []*PublicKey, signature *Signature, payloadBytes []byte) (bool, error) {
	var flowPublicKeys []flowCrypto.PublicKey
	for _, publicKey := range publicKeys {
		flowPublicKeys = append(flowPublicKeys, publicKey.flowPublicKey)
	}
	return flowCrypto.VerifyBLSSignatureOneMessage(flowPublicKeys, signature.flowSignature, payloadBytes, HashingAlgorithm)
}

//
// TYPES: PrivateKey
//

type PrivateKey struct {
	flowPrivateKey flowCrypto.PrivateKey
}

func (privateKey *PrivateKey) Sign(payloadBytes []byte) (*Signature, error) {
	if privateKey.flowPrivateKey == nil {
		return nil, errors.New("bls.PrivateKey is nil")
	}
	flowSignature, err := privateKey.flowPrivateKey.Sign(payloadBytes, HashingAlgorithm)
	if err != nil {
		return nil, err
	}
	return &Signature{flowSignature: flowSignature}, nil
}

func (privateKey *PrivateKey) PublicKey() *PublicKey {
	if privateKey.flowPrivateKey == nil {
		return nil
	}
	return &PublicKey{flowPublicKey: privateKey.flowPrivateKey.PublicKey()}
}

func (privateKey *PrivateKey) ToString() string {
	if privateKey.flowPrivateKey == nil {
		return ""
	}
	return privateKey.flowPrivateKey.String()
}

func (privateKey *PrivateKey) FromString(privateKeyString string) (*PrivateKey, error) {
	if privateKeyString == "" {
		return nil, errors.New("empty bls.PrivateKey string provided")
	}
	// Chop off leading 0x, if exists. Otherwise, does nothing.
	privateKeyStringCopy, _ := strings.CutPrefix(privateKeyString, "0x")
	// Convert from hex string to byte slice.
	privateKeyBytes, err := hex.DecodeString(privateKeyStringCopy)
	if err != nil {
		return nil, err
	}
	// Convert from byte slice to bls.PrivateKey.
	privateKey.flowPrivateKey, err = flowCrypto.DecodePrivateKey(SigningAlgorithm, privateKeyBytes)
	return privateKey, err
}

func (privateKey *PrivateKey) Eq(other *PrivateKey) bool {
	if privateKey.flowPrivateKey == nil || other == nil {
		return false
	}
	return privateKey.flowPrivateKey.Equals(other.flowPrivateKey)
}

//
// TYPES: PublicKey
//

type PublicKey struct {
	flowPublicKey flowCrypto.PublicKey
}

func (publicKey *PublicKey) Verify(signature *Signature, input []byte) (bool, error) {
	if publicKey.flowPublicKey == nil {
		return false, errors.New("bls.PublicKey is nil")
	}
	return publicKey.flowPublicKey.Verify(signature.flowSignature, input, HashingAlgorithm)
}

func (publicKey *PublicKey) ToBytes() []byte {
	var publicKeyBytes []byte
	if publicKey.flowPublicKey != nil {
		publicKeyBytes = publicKey.flowPublicKey.Encode()
	}
	return publicKeyBytes
}

func (publicKey *PublicKey) FromBytes(publicKeyBytes []byte) (*PublicKey, error) {
	if len(publicKeyBytes) == 0 {
		return nil, errors.New("empty bls.PublicKey bytes provided")
	}
	var err error
	publicKey.flowPublicKey, err = flowCrypto.DecodePublicKey(SigningAlgorithm, publicKeyBytes)
	return publicKey, err
}

func (publicKey *PublicKey) ToString() string {
	if publicKey.flowPublicKey == nil {
		return ""
	}
	return publicKey.flowPublicKey.String()
}

func (publicKey *PublicKey) FromString(publicKeyString string) (*PublicKey, error) {
	if publicKeyString == "" {
		return nil, errors.New("empty bls.PublicKey string provided")
	}
	// Chop off leading 0x, if exists. Otherwise, does nothing.
	publicKeyStringCopy, _ := strings.CutPrefix(publicKeyString, "0x")
	// Convert from hex string to byte slice.
	publicKeyBytes, err := hex.DecodeString(publicKeyStringCopy)
	if err != nil {
		return nil, err
	}
	// Convert from byte slice to bls.PublicKey.
	publicKey.flowPublicKey, err = flowCrypto.DecodePublicKey(SigningAlgorithm, publicKeyBytes)
	return publicKey, err
}

func (publicKey *PublicKey) Eq(other *PublicKey) bool {
	if publicKey.flowPublicKey == nil || other == nil {
		return false
	}
	return publicKey.flowPublicKey.Equals(other.flowPublicKey)
}

func (publicKey *PublicKey) Copy() *PublicKey {
	return &PublicKey{
		flowPublicKey: publicKey.flowPublicKey,
	}
}

//
// TYPES: Signature
//

type Signature struct {
	flowSignature flowCrypto.Signature
}

func (signature *Signature) ToBytes() []byte {
	var signatureBytes []byte
	if signature.flowSignature != nil {
		signatureBytes = signature.flowSignature.Bytes()
	}
	return signatureBytes
}

func (signature *Signature) FromBytes(signatureBytes []byte) (*Signature, error) {
	if len(signatureBytes) == 0 {
		return nil, errors.New("empty bls.Signature bytes provided")
	}
	signature.flowSignature = signatureBytes
	return signature, nil
}

func (signature *Signature) ToString() string {
	if signature.flowSignature == nil {
		return ""
	}
	return signature.flowSignature.String()
}

func (signature *Signature) FromString(signatureString string) (*Signature, error) {
	if signatureString == "" {
		return nil, errors.New("empty bls.Signature string provided")
	}
	// Chop off leading 0x, if exists. Otherwise, does nothing.
	signatureStringCopy, _ := strings.CutPrefix(signatureString, "0x")
	// Convert from hex string to byte slice.
	signatureBytes, err := hex.DecodeString(signatureStringCopy)
	if err != nil {
		return nil, err
	}
	// Convert from byte slice to bls.Signature.
	signature.flowSignature = signatureBytes
	return signature, nil
}

func (signature *Signature) Eq(other *Signature) bool {
	if signature.flowSignature == nil || other == nil {
		return false
	}
	return bytes.Equal(signature.ToBytes(), other.ToBytes())
}

func (signature *Signature) Copy() *Signature {
	if signature.flowSignature == nil {
		return &Signature{}
	}
	return &Signature{
		flowSignature: append([]byte{}, signature.flowSignature.Bytes()...),
	}
}
