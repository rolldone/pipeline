package util

import (
	"fmt"
	"path"
	"path/filepath"
	"strings"
)

// LocalToRemote converts an absolute local path (on host) to a POSIX remote path
// by computing the relative path from localBase, converting separators to '/'
// and joining with remoteBase. Returns an error if the relative path cannot be determined.
func LocalToRemote(localBase, remoteBase, absLocalPath string) (string, error) {
	rel, err := filepath.Rel(localBase, absLocalPath)
	if err != nil {
		return "", fmt.Errorf("failed to compute relative path from %s to %s: %v", localBase, absLocalPath, err)
	}

	relPosix := filepath.ToSlash(rel)
	relPosix = path.Clean(relPosix)

	if relPosix == "." {
		return path.Clean(remoteBase), nil
	}
	// join remoteBase and relPosix ensuring single '/'
	if remoteBase == "" || remoteBase == "." {
		return relPosix, nil
	}
	if strings.HasSuffix(remoteBase, "/") {
		return path.Clean(remoteBase + relPosix), nil
	}
	return path.Clean(remoteBase + "/" + relPosix), nil
}

// RemoteToLocal converts a remote POSIX path to an absolute local path by
// replacing the remoteBase prefix with localBase and converting separators.
// Returns an error if the remote path does not reside under remoteBase.
func RemoteToLocal(remoteBase, localBase, remotePath string) (string, error) {
	normRemoteBase := path.Clean(strings.ReplaceAll(remoteBase, "\\", "/"))
	normRemotePath := path.Clean(strings.ReplaceAll(remotePath, "\\", "/"))

	if normRemoteBase != "." && !strings.HasPrefix(normRemotePath, normRemoteBase) {
		return "", fmt.Errorf("remote path %s is not under remote base %s", normRemotePath, normRemoteBase)
	}

	rel := strings.TrimPrefix(normRemotePath, normRemoteBase)
	rel = strings.TrimPrefix(rel, "/")

	localRel := filepath.FromSlash(rel)
	localPath := filepath.Join(localBase, localRel)
	return localPath, nil
}
