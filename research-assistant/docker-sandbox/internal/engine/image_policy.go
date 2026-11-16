package engine

import (
	_ "crypto/sha256" // register the only supported content digest algorithm
	"fmt"
	"os"
	"strings"

	"github.com/distribution/reference"
	"github.com/opencontainers/go-digest"
)

const defaultImageAllowlist = "docker.io/library/python:3.9-bullseye,docker.io/library/python:3.11-bullseye"

type imageReference struct {
	named      reference.Named
	repository string
	tag        string
	digest     digest.Digest
}

func parseImageReference(raw string) (imageReference, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return imageReference{}, fmt.Errorf("image reference is empty")
	}
	named, err := reference.ParseNormalizedNamed(raw)
	if err != nil {
		return imageReference{}, fmt.Errorf("invalid image reference %q: %w", raw, err)
	}
	// Registry hosts are case-insensitive; repository paths and tags retain
	// the reference parser's semantics (in particular, tags are case-sensitive).
	domain, path := strings.ToLower(reference.Domain(named)), reference.Path(named)
	if domain == "index.docker.io" || domain == "registry-1.docker.io" {
		domain = "docker.io"
	}
	base, err := reference.WithName(domain + "/" + path)
	if err != nil {
		return imageReference{}, err
	}
	parsed := imageReference{named: base, repository: base.Name()}
	if tagged, ok := named.(reference.NamedTagged); ok {
		parsed.tag = tagged.Tag()
		parsed.named, err = reference.WithTag(parsed.named, parsed.tag)
		if err != nil {
			return imageReference{}, err
		}
	}
	if canonical, ok := named.(reference.Canonical); ok {
		parsed.digest = canonical.Digest()
		if parsed.digest.Algorithm() != digest.SHA256 || parsed.digest.Validate() != nil {
			return imageReference{}, fmt.Errorf("image digest must be a valid sha256 digest")
		}
		parsed.named, err = reference.WithDigest(parsed.named, parsed.digest)
		if err != nil {
			return imageReference{}, err
		}
	}
	if parsed.tag == "" && parsed.digest == "" {
		return imageReference{}, fmt.Errorf("image %q requires an explicit tag or digest", raw)
	}
	return parsed, nil
}

type imagePolicy struct {
	rules  []imageReference
	mirror string // canonical registry[:port][/prefix], Docker Hub only
}

func loadImagePolicy() (imagePolicy, error) {
	raw, configured := os.LookupEnv("SANDBOX_IMAGE_ALLOWLIST")
	if !configured {
		raw = defaultImageAllowlist
	}
	if strings.TrimSpace(raw) == "" {
		return imagePolicy{}, fmt.Errorf("image_policy_invalid: SANDBOX_IMAGE_ALLOWLIST is explicitly empty")
	}
	policy := imagePolicy{}
	for _, entry := range strings.Split(raw, ",") {
		rule, err := parseImageReference(entry)
		if err != nil {
			return imagePolicy{}, fmt.Errorf("image_policy_invalid: SANDBOX_IMAGE_ALLOWLIST: %w", err)
		}
		policy.rules = append(policy.rules, rule)
	}
	mirror, err := parseRegistryMirror(os.Getenv("SANDBOX_REGISTRY_MIRROR"))
	if err != nil {
		return imagePolicy{}, fmt.Errorf("image_policy_invalid: %w", err)
	}
	policy.mirror = mirror
	if mirror != "" {
		for _, rule := range policy.rules {
			if strings.HasPrefix(rule.repository, mirror+"/") {
				return imagePolicy{}, fmt.Errorf("image_policy_invalid: allowlist entries under SANDBOX_REGISTRY_MIRROR must use their logical Docker Hub source")
			}
		}
	}
	return policy, nil
}

func parseRegistryMirror(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	// This is a registry mapping, not an arbitrary URL. Credentials, queries,
	// fragments and schemes must not silently change the trusted destination.
	if strings.ContainsAny(raw, "@?#\\") || strings.Contains(raw, "://") || strings.HasSuffix(raw, "/") {
		return "", fmt.Errorf("SANDBOX_REGISTRY_MIRROR must be registry[:port][/prefix], without URL syntax or trailing slash")
	}
	first := strings.SplitN(raw, "/", 2)[0]
	if !strings.ContainsAny(first, ".:") && first != "localhost" {
		return "", fmt.Errorf("SANDBOX_REGISTRY_MIRROR requires an explicit registry host")
	}
	named, err := reference.ParseNormalizedNamed(raw + "/security-policy-probe:validation")
	if err != nil {
		return "", fmt.Errorf("invalid SANDBOX_REGISTRY_MIRROR: %w", err)
	}
	host := strings.ToLower(reference.Domain(named))
	if host == "docker.io" || host == "index.docker.io" || host == "registry-1.docker.io" {
		return "", fmt.Errorf("SANDBOX_REGISTRY_MIRROR must identify a distinct trusted mirror")
	}
	path := strings.TrimSuffix(reference.Path(named), "/security-policy-probe")
	if path == "security-policy-probe" {
		return host, nil
	}
	return host + "/" + path, nil
}

func (p imagePolicy) allows(ref imageReference) bool {
	for _, rule := range p.rules {
		if ref.repository == rule.repository && ref.tag == rule.tag && ref.digest == rule.digest {
			return true
		}
	}
	return false
}

type authorizedImage struct {
	logical  imageReference
	resolved imageReference
}

func (p imagePolicy) authorize(image string) (authorizedImage, error) {
	logical, err := parseImageReference(image)
	if err != nil {
		return authorizedImage{}, fmt.Errorf("image_not_allowed: %w", err)
	}
	// Already-mapped references are accepted only under the configured mirror
	// prefix, and their logical Docker Hub source is checked independently.
	if p.mirror != "" && strings.HasPrefix(logical.repository, p.mirror+"/") {
		logical, err = parseImageReference("docker.io/" + strings.TrimPrefix(logical.named.String(), p.mirror+"/"))
		if err != nil {
			return authorizedImage{}, fmt.Errorf("image_not_allowed: invalid mirrored source: %w", err)
		}
	}
	if !p.allows(logical) {
		return authorizedImage{}, fmt.Errorf("image_not_allowed: %s is not an exact SANDBOX_IMAGE_ALLOWLIST reference", logical.named.String())
	}
	resolved := logical
	if p.mirror != "" && reference.Domain(logical.named) == "docker.io" {
		resolved, err = parseImageReference(p.mirror + "/" + strings.TrimPrefix(logical.named.String(), "docker.io/"))
		if err != nil {
			return authorizedImage{}, fmt.Errorf("image_not_allowed: invalid mirror mapping: %w", err)
		}
	}
	return authorizedImage{logical: logical, resolved: resolved}, nil
}

// ValidateImagePolicy validates operator configuration without invoking Docker.
// Native-only execution does not need this container policy.
func ValidateImagePolicy() error {
	_, err := loadImagePolicy()
	return err
}

// AuthorizeImage validates the logical source before an external provider can
// perform any image-related side effect. Registry mirrors are local Docker
// transport mappings; remote providers receive the canonical logical source.
func AuthorizeImage(image string) (string, error) {
	policy, err := loadImagePolicy()
	if err != nil {
		return "", err
	}
	authorized, err := policy.authorize(image)
	if err != nil {
		return "", err
	}
	return authorized.logical.named.String(), nil
}

func authorizeDockerImage(image string) (authorizedImage, error) {
	policy, err := loadImagePolicy()
	if err != nil {
		return authorizedImage{}, err
	}
	return policy.authorize(image)
}
