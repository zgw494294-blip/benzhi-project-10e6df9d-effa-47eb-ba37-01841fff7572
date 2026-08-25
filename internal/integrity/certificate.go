package integrity

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"sort"
	"time"

	"stage-rigging-clearance/internal/domain"
)

type certificateContent struct {
	ID             string `json:"id"`
	SessionID      string `json:"sessionId"`
	Sequence       int64  `json:"sequence"`
	ManifestDigest string `json:"manifestDigest"`
	PreviousDigest string `json:"previousDigest"`
	ApprovedBy     string `json:"approvedBy"`
	IssuedAt       string `json:"issuedAt"`
}

type certificateDigester struct {
	hasher  hash.Hash
	scratch []byte
}

var reusableCertificateDigester = certificateDigester{hasher: sha256.New()}

func (d *certificateDigester) digest(content certificateContent) string {
	d.scratch, _ = json.Marshal(content)
	d.hasher.Reset()
	_, _ = d.hasher.Write(d.scratch)
	return hex.EncodeToString(d.hasher.Sum(nil))
}

func CertificateDigest(c domain.Certificate) string {
	content := certificateContent{ID: c.ID, SessionID: c.SessionID, Sequence: c.Sequence, ManifestDigest: c.ManifestDigest, PreviousDigest: c.PreviousDigest, ApprovedBy: c.ApprovedBy, IssuedAt: c.IssuedAt.UTC().Format(time.RFC3339Nano)}
	return reusableCertificateDigester.digest(content)
}

func SignCertificate(c domain.Certificate) domain.Certificate {
	c.CertificateDigest = CertificateDigest(c)
	return c
}

type VerificationResult struct {
	Valid   bool     `json:"valid"`
	Checked int      `json:"checked"`
	Errors  []string `json:"errors"`
}

func VerifyChain(certificates []domain.Certificate, expectedManifest string) VerificationResult {
	r := VerifyLedger(certificates)
	for _, c := range certificates {
		if c.ManifestDigest != expectedManifest {
			r.Errors = append(r.Errors, fmt.Sprintf("凭据 %d 清单摘要不匹配", c.Sequence))
		}
	}
	r.Valid = len(r.Errors) == 0
	return r
}

// VerifyLedger 验证跨批次的全局凭据链。每张凭据可对应不同的冻结清单，
// 但序号、前序摘要和自身内容摘要必须连续且一致。
func VerifyLedger(certificates []domain.Certificate) VerificationResult {
	r := VerificationResult{Valid: true}
	ordered := append([]domain.Certificate(nil), certificates...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Sequence < ordered[j].Sequence })
	previous := ""
	var sequence int64
	for _, c := range ordered {
		r.Checked++
		if c.Sequence != sequence+1 {
			r.Errors = append(r.Errors, fmt.Sprintf("序号不连续：期望 %d，实际 %d", sequence+1, c.Sequence))
		}
		if c.PreviousDigest != previous {
			r.Errors = append(r.Errors, fmt.Sprintf("凭据 %d 前序摘要不匹配", c.Sequence))
		}
		if CertificateDigest(c) != c.CertificateDigest {
			r.Errors = append(r.Errors, fmt.Sprintf("凭据 %d 内容摘要无效", c.Sequence))
		}
		sequence = c.Sequence
		previous = c.CertificateDigest
	}
	if len(ordered) == 0 {
		r.Errors = append(r.Errors, "尚未签发凭据")
	}
	r.Valid = len(r.Errors) == 0
	return r
}
