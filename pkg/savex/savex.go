// Package savex 提供玩家存档的 protobuf 与 gzip 编解码能力。
package savex

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"

	"google.golang.org/protobuf/proto"
)

// maxDecompressedBytes 限制单份存档解压后的最大字节数，防止恶意 gzip 数据耗尽内存。
const maxDecompressedBytes = 8 << 20

// CompressWire 将原始字节使用 gzip 最优压缩级别编码为存档线格式。
func CompressWire(plain []byte) ([]byte, error) {
	return compress(plain)
}

// DecompressWire 将 gzip 存档线格式还原为原始字节。
func DecompressWire(wire []byte) ([]byte, error) {
	return decompress(wire)
}

// Encode 将 protobuf 消息序列化后进行 gzip 压缩，返回存档线格式字节。
func Encode(msg proto.Message) ([]byte, error) {
	raw, err := proto.Marshal(msg)
	if err != nil {
		return nil, fmt.Errorf("savex: proto marshal failed: %w", err)
	}
	return CompressWire(raw)
}

// Decode 将 gzip 存档线格式解压并反序列化为指定 protobuf 消息。
// newMsg 每次调用都必须返回新的消息实例。
func Decode[M proto.Message](wire []byte, newMsg func() M) (M, error) {
	var zero M
	raw, err := DecompressWire(wire)
	if err != nil {
		return zero, err
	}
	msg := newMsg()
	if err := proto.Unmarshal(raw, msg); err != nil {
		return zero, fmt.Errorf("savex: proto unmarshal failed: %w", err)
	}
	return msg, nil
}

// ComputeSign 计算存档线格式字节的 SHA256 小写十六进制签名。
func ComputeSign(wire []byte) string {
	sum := sha256.Sum256(wire)
	return hex.EncodeToString(sum[:])
}

// compress 使用 gzip 最优压缩级别压缩数据。
func compress(data []byte) ([]byte, error) {
	var buffer bytes.Buffer
	writer, err := gzip.NewWriterLevel(&buffer, gzip.BestCompression)
	if err != nil {
		return nil, fmt.Errorf("savex: create gzip writer failed: %w", err)
	}
	if _, err := writer.Write(data); err != nil {
		_ = writer.Close()
		return nil, fmt.Errorf("savex: gzip compress failed: %w", err)
	}
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("savex: gzip close failed: %w", err)
	}
	return buffer.Bytes(), nil
}

// decompress 解压 gzip 数据，并限制解压后的字节数量。
func decompress(data []byte) ([]byte, error) {
	reader, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("savex: create gzip reader failed: %w", err)
	}
	defer reader.Close()

	raw, err := io.ReadAll(io.LimitReader(reader, maxDecompressedBytes+1))
	if err != nil {
		return nil, fmt.Errorf("savex: gzip decompress failed: %w", err)
	}
	if len(raw) > maxDecompressedBytes {
		return nil, fmt.Errorf("savex: decompressed data exceeds %d bytes", maxDecompressedBytes)
	}
	return raw, nil
}
