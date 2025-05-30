package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"time"

	"github.com/AdguardTeam/golibs/log"
)

const (
	certPath = "ca.crt"
	keyPath  = "ca.key"
)

func init() {
	_, certErr := os.Stat(certPath)
	_, keyErr := os.Stat(keyPath)
	if !os.IsNotExist(certErr) && !os.IsNotExist(keyErr) {
		return
	}

	log.Info("Generating new CA certificate and key...")
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		panic(err)
	}

	ca := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			Organization: []string{"gitmproxy"},
			CommonName:   "Gopher in the middle Root CA",
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(365 * 24 * time.Hour * 30), // 30 years
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            2,
		MaxPathLenZero:        false,
	}

	der, err := x509.CreateCertificate(rand.Reader, ca, ca, &priv.PublicKey, priv)
	if err != nil {
		panic(err)
	}

	// Write cert
	certOut, err := os.Create(certPath)
	if err != nil {
		panic(err)
	}
	defer certOut.Close()
	if err := pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: der}); err != nil {
		panic(err)
	}

	// Write key
	keyOut, err := os.Create(keyPath)
	if err != nil {
		panic(err)
	}
	defer keyOut.Close()
	if err := pem.Encode(keyOut, &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(priv)}); err != nil {
		panic(err)
	}

	log.Info("CA certificate and key generated.")
}
