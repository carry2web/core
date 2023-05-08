//go:build !relic

package bls

const BLSNoRelicError = "BLS keys can't be used without Relic installed"

func AggregateSignatures(signatures []*Signature) (*Signature, error) {
	panic(BLSNoRelicError)
}

func VerifyAggregateSignature(publicKeys []*PublicKey, signature *Signature, payloadBytes []byte) (bool, error) {
	panic(BLSNoRelicError)
}

//
// TYPES: PrivateKey
//

type PrivateKey struct{}

func (privateKey *PrivateKey) Sign(payloadBytes []byte) (*Signature, error) {
	panic(BLSNoRelicError)
}

func (privateKey *PrivateKey) PublicKey() *PublicKey {
	panic(BLSNoRelicError)
}

func (privateKey *PrivateKey) ToString() string {
	panic(BLSNoRelicError)
}

func (privateKey *PrivateKey) FromString(privateKeyString string) (*PrivateKey, error) {
	panic(BLSNoRelicError)
}

func (privateKey *PrivateKey) Eq(other *PrivateKey) bool {
	panic(BLSNoRelicError)
}

//
// TYPES: PublicKey
//

type PublicKey struct{}

func (publicKey *PublicKey) Verify(signature *Signature, input []byte) (bool, error) {
	panic(BLSNoRelicError)
}

func (publicKey *PublicKey) ToBytes() []byte {
	panic(BLSNoRelicError)
}

func (publicKey *PublicKey) FromBytes(publicKeyBytes []byte) (*PublicKey, error) {
	panic(BLSNoRelicError)
}

func (publicKey *PublicKey) ToString() string {
	panic(BLSNoRelicError)
}

func (publicKey *PublicKey) FromString(publicKeyString string) (*PublicKey, error) {
	panic(BLSNoRelicError)
}

func (publicKey *PublicKey) Eq(other *PublicKey) bool {
	panic(BLSNoRelicError)
}

func (publicKey *PublicKey) Copy() *PublicKey {
	panic(BLSNoRelicError)
}

//
// TYPES: Signature
//

type Signature struct{}

func (signature *Signature) ToBytes() []byte {
	panic(BLSNoRelicError)
}

func (signature *Signature) FromBytes(signatureBytes []byte) (*Signature, error) {
	panic(BLSNoRelicError)
}

func (signature *Signature) ToString() string {
	panic(BLSNoRelicError)
}

func (signature *Signature) FromString(signatureString string) (*Signature, error) {
	panic(BLSNoRelicError)
}

func (signature *Signature) Eq(other *Signature) bool {
	panic(BLSNoRelicError)
}

func (signature *Signature) Copy() *Signature {
	panic(BLSNoRelicError)
}
