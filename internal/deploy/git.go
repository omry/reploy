package deploy

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
)

var fullGitHashPattern = regexp.MustCompile(`^[0-9a-fA-F]{40}$`)

func loadGitPack(ref PackRef) (AppPack, error) {
	if ref.Scheme != "git" {
		return AppPack{}, fmt.Errorf("blueprint scheme is not git: %s", ref.Scheme)
	}
	if err := validateGitPackRef(ref); err != nil {
		return AppPack{}, err
	}
	checkoutRoot, commit, err := cacheGitCheckout(ref.Source, ref.Query.Get("ref"))
	if err != nil {
		return AppPack{}, err
	}
	pack, subdir, err := loadSourceCheckout(checkoutRoot, ref.Subdir)
	if err != nil {
		return AppPack{}, err
	}
	resolvedRef := PackRef{
		Scheme:   "git",
		Source:   ref.Source,
		Subdir:   subdir,
		Query:    url.Values{"ref": []string{commit}},
		IsPinned: true,
	}
	resolvedRef.Raw = "git:" + resolvedRef.Source + "#" + resolvedRef.Subdir + "?ref=" + commit
	pack.Ref = resolvedRef
	pack.RequestedRef = ref
	pack.ResolvedArtifact = &ResolvedPackArtifact{
		Scheme:        "git",
		Package:       ref.Source,
		Version:       commit,
		Subdir:        subdir,
		CachePath:     checkoutRoot,
		BlueprintPath: pack.Dir,
	}
	return pack, nil
}

func validateGitPackRef(ref PackRef) error {
	parsed, err := url.Parse(ref.Source)
	if err != nil || parsed.Scheme == "" {
		return fmt.Errorf("git blueprint source must be a URL: %s", ref.Source)
	}
	if parsed.Scheme == "https" && parsed.Host == "" {
		return fmt.Errorf("git blueprint source must be a URL: %s", ref.Source)
	}
	if parsed.Scheme == "file" && parsed.Path == "" {
		return fmt.Errorf("git blueprint source must be a URL: %s", ref.Source)
	}
	if parsed.Scheme != "https" && parsed.Scheme != "file" {
		return fmt.Errorf("git blueprint source must use https: %s", ref.Source)
	}
	for key := range ref.Query {
		if key != "ref" {
			return fmt.Errorf("unsupported git blueprint query parameter: %s", key)
		}
	}
	if len(ref.Query["ref"]) > 1 {
		return fmt.Errorf("git blueprint ref query must be specified at most once")
	}
	return nil
}

func cacheGitCheckout(sourceURL string, revision string) (string, string, error) {
	cacheRoot, err := reployCacheDir()
	if err != nil {
		return "", "", err
	}
	if isFullGitHash(revision) {
		checkoutRoot := gitCheckoutCachePath(cacheRoot, sourceURL, strings.ToLower(revision))
		if info, err := os.Stat(checkoutRoot); err == nil && info.IsDir() {
			return checkoutRoot, strings.ToLower(revision), nil
		}
	}
	tmpRoot := filepath.Join(cacheRoot, "tmp")
	if err := os.MkdirAll(tmpRoot, 0o755); err != nil {
		return "", "", err
	}
	tempDir, err := os.MkdirTemp(tmpRoot, "git-*")
	if err != nil {
		return "", "", err
	}
	defer os.RemoveAll(tempDir)

	cloneDir := filepath.Join(tempDir, "checkout")
	repository, err := cloneGitRevision(sourceURL, revision, cloneDir)
	if err != nil {
		return "", "", err
	}
	head, err := repository.Head()
	if err != nil {
		return "", "", fmt.Errorf("resolve git HEAD: %w", err)
	}
	commit := head.Hash().String()
	checkoutRoot := gitCheckoutCachePath(cacheRoot, sourceURL, commit)
	if info, err := os.Stat(checkoutRoot); err == nil && info.IsDir() {
		return checkoutRoot, commit, nil
	} else if err != nil && !os.IsNotExist(err) {
		return "", "", err
	}
	if err := os.MkdirAll(filepath.Dir(checkoutRoot), 0o755); err != nil {
		return "", "", err
	}
	if err := os.Rename(cloneDir, checkoutRoot); err != nil {
		if info, statErr := os.Stat(checkoutRoot); statErr == nil && info.IsDir() {
			return checkoutRoot, commit, nil
		}
		return "", "", err
	}
	return checkoutRoot, commit, nil
}

func cloneGitRevision(sourceURL string, revision string, cloneDir string) (*git.Repository, error) {
	if strings.TrimSpace(revision) == "" {
		return git.PlainClone(cloneDir, false, &git.CloneOptions{
			URL:   sourceURL,
			Depth: 1,
		})
	}
	var firstErr error
	for _, referenceName := range gitReferenceCandidates(revision) {
		if err := os.RemoveAll(cloneDir); err != nil {
			return nil, err
		}
		repository, err := git.PlainClone(cloneDir, false, &git.CloneOptions{
			URL:           sourceURL,
			ReferenceName: referenceName,
			SingleBranch:  true,
			Depth:         1,
		})
		if err == nil {
			return repository, nil
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	if err := os.RemoveAll(cloneDir); err != nil {
		return nil, err
	}
	repository, err := git.PlainClone(cloneDir, false, &git.CloneOptions{URL: sourceURL})
	if err != nil {
		return nil, fmt.Errorf("clone git repository: %w", err)
	}
	hash, err := resolveGitRevision(repository, revision)
	if err != nil {
		if firstErr != nil {
			return nil, fmt.Errorf("resolve git ref %q after shallow clone failed (%v): %w", revision, firstErr, err)
		}
		return nil, err
	}
	worktree, err := repository.Worktree()
	if err != nil {
		return nil, err
	}
	if err := worktree.Checkout(&git.CheckoutOptions{Hash: *hash, Force: true}); err != nil {
		return nil, fmt.Errorf("checkout git ref %q: %w", revision, err)
	}
	return repository, nil
}

func gitReferenceCandidates(revision string) []plumbing.ReferenceName {
	revision = strings.TrimSpace(revision)
	if revision == "" || isFullGitHash(revision) {
		return nil
	}
	if strings.HasPrefix(revision, "refs/") {
		return []plumbing.ReferenceName{plumbing.ReferenceName(revision)}
	}
	return []plumbing.ReferenceName{
		plumbing.NewBranchReferenceName(revision),
		plumbing.NewTagReferenceName(revision),
	}
}

func resolveGitRevision(repository *git.Repository, revision string) (*plumbing.Hash, error) {
	revision = strings.TrimSpace(revision)
	if isFullGitHash(revision) {
		hash := plumbing.NewHash(revision)
		if _, err := repository.CommitObject(hash); err != nil {
			return nil, fmt.Errorf("resolve git commit %q: %w", revision, err)
		}
		return &hash, nil
	}
	candidates := []plumbing.Revision{
		plumbing.Revision(revision),
		plumbing.Revision("refs/heads/" + revision),
		plumbing.Revision("refs/tags/" + revision),
		plumbing.Revision("origin/" + revision),
	}
	for _, candidate := range candidates {
		hash, err := repository.ResolveRevision(candidate)
		if err == nil {
			return hash, nil
		}
	}
	return nil, fmt.Errorf("resolve git ref %q", revision)
}

func gitCheckoutCachePath(cacheRoot string, sourceURL string, commit string) string {
	return filepath.Join(cacheRoot, "git", HashBytes([]byte(sourceURL)), commit)
}

func isFullGitHash(value string) bool {
	return fullGitHashPattern.MatchString(strings.TrimSpace(value))
}
