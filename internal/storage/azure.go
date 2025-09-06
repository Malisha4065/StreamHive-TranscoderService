package storage

import (
	"context"
	"fmt"
	"io/fs"
	"io/ioutil"
	"mime"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
	"strconv"

	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/blob"
	"github.com/sony/gobreaker"
)

// Helper function to read secret from file or fallback to environment variable
func getSecret(filePath, envVar string) string {
	if data, err := ioutil.ReadFile(filePath); err == nil {
		return strings.TrimSpace(string(data))
	}
	return os.Getenv(envVar)
}

type AzureClient struct {
	service   *azblob.Client
	container string
	breaker   *gobreaker.CircuitBreaker
}

func NewAzureClientFromEnv() (*AzureClient, error) {
	container := getSecret("/mnt/secrets-store/azure-storage-raw-container", "AZURE_BLOB_CONTAINER")
	if container == "" {
		container = "uploadservicecontainer"
	}
	acct := getSecret("/mnt/secrets-store/azure-storage-account", "AZURE_STORAGE_ACCOUNT")
	sas := os.Getenv("AZURE_STORAGE_SAS_URL")
	key := getSecret("/mnt/secrets-store/azure-storage-key", "AZURE_STORAGE_KEY")

	var svc *azblob.Client
	if sas != "" {
		u, err := url.Parse(sas)
		if err != nil {
			return nil, fmt.Errorf("invalid SAS url: %w", err)
		}
		svc, err = azblob.NewClientWithNoCredential(u.String(), nil)
		if err != nil {
			return nil, err
		}
	} else {
		cred, err := azblob.NewSharedKeyCredential(acct, key)
		if err != nil {
			return nil, err
		}
		u := fmt.Sprintf("https://%s.blob.core.windows.net/", acct)
		svc, err = azblob.NewClientWithSharedKeyCredential(u, cred, nil)
		if err != nil {
			return nil, err
		}
	}
	cbTimeout := 10 * time.Second
	if v := os.Getenv("TRANSCODER_CB_RESET_MS"); v != "" { if d, err := time.ParseDuration(v+"ms"); err == nil { cbTimeout = d } }
	cbFailures := uint32(5)
	if v := os.Getenv("TRANSCODER_CB_CONSECUTIVE_FAILS"); v != "" { if n, err := strconv.Atoi(v); err == nil && n > 0 { cbFailures = uint32(n) } }
	breaker := gobreaker.NewCircuitBreaker(gobreaker.Settings{ Name: "azure-storage", Timeout: cbTimeout, ReadyToTrip: func(c gobreaker.Counts) bool { return c.ConsecutiveFailures >= cbFailures } })
	return &AzureClient{service: svc, container: container, breaker: breaker}, nil
}

func (c *AzureClient) DownloadTo(ctx context.Context, blobPath, localPath string) error {
	if err := os.MkdirAll(filepath.Dir(localPath), 0o755); err != nil {
		return err
	}
	f, err := os.Create(filepath.Clean(localPath))
	if err != nil {
		return err
	}
	defer f.Close()
	attemptTimeout := 5 * time.Second
	if v := os.Getenv("TRANSCODER_AZURE_TIMEOUT_MS"); v != "" { if d, err := time.ParseDuration(v+"ms"); err == nil { attemptTimeout = d } }
	retries := 2
	if v := os.Getenv("TRANSCODER_AZURE_RETRIES"); v != "" { if n, err := strconv.Atoi(v); err == nil && n >= 0 { retries = n } }
	var last error
	backoff := 200 * time.Millisecond
	for i := 0; i <= retries; i++ {
		dctx, cancel := context.WithTimeout(ctx, attemptTimeout)
		_, err = c.breaker.Execute(func() (interface{}, error) { return c.service.DownloadFile(dctx, c.container, blobPath, f, nil) })
		cancel()
		if err == nil { return nil }
		last = err
		if i < retries { time.Sleep(backoff); if backoff < 1500*time.Millisecond { backoff *= 2 } }
	}
	return last
}

func (c *AzureClient) UploadFile(ctx context.Context, localPath, blobPath string, contentType string) error {
	f, err := os.Open(filepath.Clean(localPath))
	if err != nil {
		return err
	}
	defer f.Close()
	headers := &blob.HTTPHeaders{BlobContentType: &contentType}
	attemptTimeout := 10 * time.Second
	if v := os.Getenv("TRANSCODER_AZURE_TIMEOUT_MS"); v != "" { if d, err := time.ParseDuration(v+"ms"); err == nil { attemptTimeout = d } }
	retries := 2
	if v := os.Getenv("TRANSCODER_AZURE_RETRIES"); v != "" { if n, err := strconv.Atoi(v); err == nil && n >= 0 { retries = n } }
	var last error
	backoff := 200 * time.Millisecond
	for i := 0; i <= retries; i++ {
		uctx, cancel := context.WithTimeout(ctx, attemptTimeout)
		_, err = c.breaker.Execute(func() (interface{}, error) { return c.service.UploadFile(uctx, c.container, blobPath, f, &azblob.UploadFileOptions{HTTPHeaders: headers}) })
		cancel()
		if err == nil { return nil }
		last = err
		if i < retries { time.Sleep(backoff); if backoff < 1500*time.Millisecond { backoff *= 2 } }
	}
	return last
}

func (c *AzureClient) UploadDir(ctx context.Context, localRoot, blobPrefix string) error {
	return filepath.WalkDir(localRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(localRoot, path)
		if err != nil {
			return err
		}
		blobName := filepath.ToSlash(filepath.Join(blobPrefix, rel))
		ct := detectContentType(path)
		return c.UploadFile(ctx, path, blobName, ct)
	})
}

func detectContentType(path string) string {
	low := strings.ToLower(path)
	if strings.HasSuffix(low, ".m3u8") {
		return "application/vnd.apple.mpegurl"
	}
	if strings.HasSuffix(low, ".ts") {
		return "video/MP2T"
	}
	if strings.HasSuffix(low, ".jpg") || strings.HasSuffix(low, ".jpeg") {
		return "image/jpeg"
	}
	if strings.HasSuffix(low, ".png") {
		return "image/png"
	}
	if ct := mime.TypeByExtension(filepath.Ext(low)); ct != "" {
		return ct
	}
	return "application/octet-stream"
}

// DeleteBlob deletes a single blob from Azure storage
func (c *AzureClient) DeleteBlob(ctx context.Context, blobPath string) error {
	_, err := c.service.DeleteBlob(ctx, c.container, blobPath, nil)
	return err
}

// DeleteBlobsWithPrefix deletes all blobs with the given prefix from Azure storage
func (c *AzureClient) DeleteBlobsWithPrefix(ctx context.Context, prefix string) error {
	pager := c.service.NewListBlobsFlatPager(c.container, &azblob.ListBlobsFlatOptions{
		Prefix: &prefix,
	})

	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return fmt.Errorf("failed to list blobs with prefix %s: %w", prefix, err)
		}

		for _, blob := range page.Segment.BlobItems {
			if blob.Name != nil {
				if err := c.DeleteBlob(ctx, *blob.Name); err != nil {
					return fmt.Errorf("failed to delete blob %s: %w", *blob.Name, err)
				}
			}
		}
	}

	return nil
}

// BlobExists checks if a blob exists in Azure storage
func (c *AzureClient) BlobExists(ctx context.Context, blobPath string) (bool, error) {
	// Try to get blob properties to check if it exists
	pager := c.service.NewListBlobsFlatPager(c.container, &azblob.ListBlobsFlatOptions{
		Prefix: &blobPath,
	})

	if pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return false, fmt.Errorf("failed to check blob existence: %w", err)
		}

		for _, blob := range page.Segment.BlobItems {
			if blob.Name != nil && *blob.Name == blobPath {
				return true, nil
			}
		}
	}

	return false, nil
}
