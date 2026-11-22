// Package database 负责 SQLite 连接、自动建表与演示种子数据写入。
package database

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/nexus-stream/nexus-core/internal/model"
)

// 演示环境默认种子数据（MVP 固定值，保证录屏效果可复现）。
const (
	SeedDeviceName = "大疆御3-巡检01 (演示通道)"
	SeedStreamKey  = "live_demo_001"
	SeedSourceURL  = "testdata/sample.mp4"
)

// Open 打开（并按需创建）SQLite 数据库，自动迁移表结构。
//
// 说明：这里使用纯 Go 实现的 sqlite 驱动（github.com/glebarez/sqlite），
// 与 gorm.io/driver/sqlite 的 API 完全一致，但无需 CGO / GCC，
// 保证在 Windows / 无编译链的演示笔记本上也能 `go run` 一把过。
func Open(path string) (*gorm.DB, error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("创建数据库目录失败: %w", err)
		}
	}

	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Warn),
	})
	if err != nil {
		return nil, fmt.Errorf("打开 SQLite 失败: %w", err)
	}

	if err := db.AutoMigrate(&model.Device{}, &model.StreamSession{}); err != nil {
		return nil, fmt.Errorf("自动迁移表结构失败: %w", err)
	}

	if err := Seed(db); err != nil {
		return nil, err
	}
	return db, nil
}

// Seed 当 devices 表为空时插入默认演示设备。
func Seed(db *gorm.DB) error {
	var count int64
	if err := db.Model(&model.Device{}).Count(&count).Error; err != nil {
		return fmt.Errorf("统计设备数量失败: %w", err)
	}
	if count > 0 {
		return nil
	}

	device := &model.Device{
		DeviceName:    SeedDeviceName,
		StreamKey:     SeedStreamKey,
		DirectionType: model.DirectionPull,
		RTSPURL:       SeedSourceURL,
		Status:        model.StatusOnline,
		LastActiveAt:  time.Now(),
	}
	if err := db.Create(device).Error; err != nil {
		return fmt.Errorf("写入种子设备失败: %w", err)
	}
	return nil
}

// PrimaryDevice 返回第一条设备记录（MVP 只有单通道）。
func PrimaryDevice(db *gorm.DB) (*model.Device, error) {
	var device model.Device
	if err := db.Order("id asc").First(&device).Error; err != nil {
		return nil, fmt.Errorf("读取设备失败: %w", err)
	}
	return &device, nil
}

// TouchDevice 刷新设备心跳与在线状态。
func TouchDevice(db *gorm.DB, id uint, status string, at time.Time) error {
	return db.Model(&model.Device{}).Where("id = ?", id).
		Updates(map[string]any{
			"status":         status,
			"last_active_at": at,
		}).Error
}

// RecordSession 写入一次转码轨道指标快照。
func RecordSession(db *gorm.DB, session *model.StreamSession) error {
	return db.Create(session).Error
}
