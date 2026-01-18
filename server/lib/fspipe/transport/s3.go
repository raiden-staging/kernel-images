package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/joho/godotenv"
	"github.com/onkernel/kernel-images/server/lib/fspipe/logging"
	"github.com/onkernel/kernel-images/server/lib/fspipe/protocol"
)

const (
	// S3 minimum part size (5MB) - except for the last part
	minPartSize = 5 * 1024 * 1024
)

// S3Config holds S3/R2 configuration
type S3Config struct {
	Endpoint        string `json:"endpoint"`
	Bucket          string `json:"bucket"`
	AccessKeyID     string `json:"access_key_id"`
	SecretAccessKey string `json:"secret_access_key"`
	Region          string `json:"region"`
	Prefix          string `json:"prefix"` // Optional path prefix
}

// S3Client manages S3/R2 uploads
type S3Client struct {
	config   S3Config
	s3Client *s3.Client

	ctx    context.Context
	cancel context.CancelFunc

	// Track multipart uploads
	mu      sync.RWMutex
	uploads map[string]*multipartUpload

	state atomic.Int32

	// Metrics
	filesCreated atomic.Uint64
	filesUploaded atomic.Uint64
	bytesUploaded atomic.Uint64
	errors        atomic.Uint64
}

type multipartUpload struct {
	key      string
	uploadID string
	parts    []types.CompletedPart
	buffer   bytes.Buffer
	partNum  int32
}

// LoadS3ConfigFromEnv loads S3 config from .env file
func LoadS3ConfigFromEnv(envFile string) (S3Config, error) {
	if envFile == "" {
		envFile = ".env"
	}

	// Load .env file if it exists
	if _, err := os.Stat(envFile); err == nil {
		if err := godotenv.Load(envFile); err != nil {
			return S3Config{}, fmt.Errorf("load .env: %w", err)
		}
	}

	cfg := S3Config{
		Endpoint:        os.Getenv("S3_ENDPOINT"),
		Bucket:          os.Getenv("S3_BUCKET"),
		AccessKeyID:     os.Getenv("S3_ACCESS_KEY_ID"),
		SecretAccessKey: os.Getenv("S3_SECRET_ACCESS_KEY"),
		Region:          os.Getenv("S3_REGION"),
		Prefix:          os.Getenv("S3_PREFIX"),
	}

	if cfg.Region == "" {
		cfg.Region = "auto" // Default for R2
	}

	return cfg, cfg.Validate()
}

// ParseS3ConfigFromJSON parses S3 config from JSON string
func ParseS3ConfigFromJSON(jsonStr string) (S3Config, error) {
	var cfg S3Config
	if err := json.Unmarshal([]byte(jsonStr), &cfg); err != nil {
		return cfg, fmt.Errorf("parse JSON: %w", err)
	}

	if cfg.Region == "" {
		cfg.Region = "auto"
	}

	return cfg, cfg.Validate()
}

// Validate checks required fields
func (c S3Config) Validate() error {
	if c.Endpoint == "" {
		return errors.New("S3_ENDPOINT is required")
	}
	if c.Bucket == "" {
		return errors.New("S3_BUCKET is required")
	}
	if c.AccessKeyID == "" {
		return errors.New("S3_ACCESS_KEY_ID is required")
	}
	if c.SecretAccessKey == "" {
		return errors.New("S3_SECRET_ACCESS_KEY is required")
	}
	return nil
}

// NewS3Client creates a new S3/R2 transport client
func NewS3Client(cfg S3Config) (*S3Client, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(context.Background())

	// Create S3 client
	awsCfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion(cfg.Region),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			cfg.AccessKeyID,
			cfg.SecretAccessKey,
			"",
		)),
	)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("load AWS config: %w", err)
	}

	s3Client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(cfg.Endpoint)
		o.UsePathStyle = true // Required for R2 and most S3-compatible storage
	})

	c := &S3Client{
		config:   cfg,
		s3Client: s3Client,
		ctx:      ctx,
		cancel:   cancel,
		uploads:  make(map[string]*multipartUpload),
	}

	c.state.Store(int32(StateConnected))
	return c, nil
}

// Connect is a no-op for S3 (already connected on creation)
func (c *S3Client) Connect() error {
	logging.Info("S3 client ready for bucket: %s", c.config.Bucket)
	return nil
}

// Send handles file operations
func (c *S3Client) Send(msgType byte, payload interface{}) error {
	switch msgType {
	case protocol.MsgFileCreate:
		msg := payload.(*protocol.FileCreate)
		return c.handleFileCreate(msg)

	case protocol.MsgWriteChunk:
		msg := payload.(*protocol.WriteChunk)
		return c.handleWriteChunk(msg)

	case protocol.MsgFileClose:
		msg := payload.(*protocol.FileClose)
		return c.handleFileClose(msg)

	case protocol.MsgRename:
		msg := payload.(*protocol.Rename)
		return c.handleRename(msg)

	case protocol.MsgDelete:
		msg := payload.(*protocol.Delete)
		return c.handleDelete(msg)

	case protocol.MsgTruncate:
		// S3 doesn't support truncate, log warning
		logging.Warn("Truncate not supported for S3")
		return nil

	default:
		return fmt.Errorf("unknown message type: 0x%02x", msgType)
	}
}

// SendAndReceive is not supported for S3 (no ACK needed)
func (c *S3Client) SendAndReceive(msgType byte, payload interface{}) (byte, []byte, error) {
	// For S3, we just send and return a fake ACK
	if err := c.Send(msgType, payload); err != nil {
		return 0, nil, err
	}

	// Return a fake ACK for write chunks
	if msgType == protocol.MsgWriteChunk {
		msg := payload.(*protocol.WriteChunk)
		ack := protocol.WriteAck{
			FileID:  msg.FileID,
			Offset:  msg.Offset,
			Written: len(msg.Data),
		}
		data, _ := json.Marshal(ack)
		return protocol.MsgWriteAck, data, nil
	}

	return 0, nil, nil
}

func (c *S3Client) handleFileCreate(msg *protocol.FileCreate) error {
	key := c.config.Prefix + msg.Filename

	c.mu.Lock()
	defer c.mu.Unlock()

	// Start multipart upload
	output, err := c.s3Client.CreateMultipartUpload(c.ctx, &s3.CreateMultipartUploadInput{
		Bucket: aws.String(c.config.Bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		c.errors.Add(1)
		return fmt.Errorf("create multipart upload: %w", err)
	}

	c.uploads[msg.FileID] = &multipartUpload{
		key:      key,
		uploadID: *output.UploadId,
		parts:    make([]types.CompletedPart, 0),
		partNum:  0,
	}

	c.filesCreated.Add(1)
	logging.Debug("S3: Started multipart upload for %s (id=%s)", key, msg.FileID)
	return nil
}

func (c *S3Client) handleWriteChunk(msg *protocol.WriteChunk) error {
	c.mu.Lock()
	upload, ok := c.uploads[msg.FileID]
	if !ok {
		c.mu.Unlock()
		return fmt.Errorf("unknown file ID: %s", msg.FileID)
	}

	// Buffer the data
	upload.buffer.Write(msg.Data)
	c.bytesUploaded.Add(uint64(len(msg.Data)))

	// If buffer >= 5MB, upload a part
	if upload.buffer.Len() >= minPartSize {
		if err := c.uploadPartLocked(upload); err != nil {
			c.mu.Unlock()
			return err
		}
	}

	c.mu.Unlock()
	return nil
}

func (c *S3Client) uploadPartLocked(upload *multipartUpload) error {
	upload.partNum++
	data := upload.buffer.Bytes()
	upload.buffer.Reset()

	output, err := c.s3Client.UploadPart(c.ctx, &s3.UploadPartInput{
		Bucket:     aws.String(c.config.Bucket),
		Key:        aws.String(upload.key),
		UploadId:   aws.String(upload.uploadID),
		PartNumber: aws.Int32(upload.partNum),
		Body:       bytes.NewReader(data),
	})
	if err != nil {
		c.errors.Add(1)
		return fmt.Errorf("upload part %d: %w", upload.partNum, err)
	}

	upload.parts = append(upload.parts, types.CompletedPart{
		ETag:       output.ETag,
		PartNumber: aws.Int32(upload.partNum),
	})

	logging.Debug("S3: Uploaded part %d (%d bytes) for %s", upload.partNum, len(data), upload.key)
	return nil
}

func (c *S3Client) handleFileClose(msg *protocol.FileClose) error {
	c.mu.Lock()
	upload, ok := c.uploads[msg.FileID]
	if !ok {
		c.mu.Unlock()
		logging.Debug("S3: FileClose for unknown ID %s", msg.FileID)
		return nil
	}
	delete(c.uploads, msg.FileID)

	// Upload remaining data as final part
	if upload.buffer.Len() > 0 {
		if err := c.uploadPartLocked(upload); err != nil {
			c.mu.Unlock()
			// Abort the upload
			c.s3Client.AbortMultipartUpload(c.ctx, &s3.AbortMultipartUploadInput{
				Bucket:   aws.String(c.config.Bucket),
				Key:      aws.String(upload.key),
				UploadId: aws.String(upload.uploadID),
			})
			return err
		}
	}

	c.mu.Unlock()

	// Complete the multipart upload
	_, err := c.s3Client.CompleteMultipartUpload(c.ctx, &s3.CompleteMultipartUploadInput{
		Bucket:   aws.String(c.config.Bucket),
		Key:      aws.String(upload.key),
		UploadId: aws.String(upload.uploadID),
		MultipartUpload: &types.CompletedMultipartUpload{
			Parts: upload.parts,
		},
	})
	if err != nil {
		c.errors.Add(1)
		// Abort on error
		c.s3Client.AbortMultipartUpload(c.ctx, &s3.AbortMultipartUploadInput{
			Bucket:   aws.String(c.config.Bucket),
			Key:      aws.String(upload.key),
			UploadId: aws.String(upload.uploadID),
		})
		return fmt.Errorf("complete multipart upload: %w", err)
	}

	c.filesUploaded.Add(1)
	logging.Info("S3: Completed upload for %s (%d parts)", upload.key, len(upload.parts))
	return nil
}

func (c *S3Client) handleRename(msg *protocol.Rename) error {
	oldKey := c.config.Prefix + msg.OldName
	newKey := c.config.Prefix + msg.NewName

	// S3 doesn't support rename, so we copy + delete
	_, err := c.s3Client.CopyObject(c.ctx, &s3.CopyObjectInput{
		Bucket:     aws.String(c.config.Bucket),
		CopySource: aws.String(c.config.Bucket + "/" + oldKey),
		Key:        aws.String(newKey),
	})
	if err != nil {
		c.errors.Add(1)
		return fmt.Errorf("copy object: %w", err)
	}

	_, err = c.s3Client.DeleteObject(c.ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(c.config.Bucket),
		Key:    aws.String(oldKey),
	})
	if err != nil {
		logging.Warn("S3: Failed to delete old key after rename: %v", err)
		// Don't return error - copy succeeded
	}

	logging.Debug("S3: Renamed %s -> %s", oldKey, newKey)
	return nil
}

func (c *S3Client) handleDelete(msg *protocol.Delete) error {
	key := c.config.Prefix + msg.Filename

	_, err := c.s3Client.DeleteObject(c.ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(c.config.Bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		c.errors.Add(1)
		return fmt.Errorf("delete object: %w", err)
	}

	logging.Debug("S3: Deleted %s", key)
	return nil
}

// State returns current state (always connected for S3)
func (c *S3Client) State() ConnectionState {
	return ConnectionState(c.state.Load())
}

// Stats returns client statistics
func (c *S3Client) Stats() map[string]uint64 {
	return map[string]uint64{
		"files_created":  c.filesCreated.Load(),
		"files_uploaded": c.filesUploaded.Load(),
		"bytes_uploaded": c.bytesUploaded.Load(),
		"errors":         c.errors.Load(),
	}
}

// Close cleans up resources
func (c *S3Client) Close() error {
	c.cancel()

	// Abort any pending uploads
	c.mu.Lock()
	defer c.mu.Unlock()

	for fileID, upload := range c.uploads {
		logging.Warn("S3: Aborting incomplete upload for %s", upload.key)
		c.s3Client.AbortMultipartUpload(c.ctx, &s3.AbortMultipartUploadInput{
			Bucket:   aws.String(c.config.Bucket),
			Key:      aws.String(upload.key),
			UploadId: aws.String(upload.uploadID),
		})
		delete(c.uploads, fileID)
	}

	return nil
}

// Compile-time interface check
var _ Transport = (*S3Client)(nil)
