// Package model 定义 Nexus-Stream 的持久化数据模型。
//
// 对应《PRD 4.1 资产与鉴权配置表》与《4.2 实时流媒体技术指标表》。
package model

import "time"

// 设备状态常量。
const (
	StatusOnline   = "ONLINE"
	StatusOffline  = "OFFLINE"
	StatusDegraded = "DEGRADED"
)

// 流量方向常量。
const (
	DirectionPull = "PULL" // 网关主动拉取 RTSP
	DirectionPush = "PUSH" // 无人机 / 车载弱网推流
)

// Device 记录允许接入的视频源（安防摄像头拉流 / 无人机推流）。
type Device struct {
	ID            uint      `gorm:"primaryKey" json:"id"`
	DeviceName    string    `gorm:"column:device_name;size:128;not null" json:"device_name"`
	StreamKey     string    `gorm:"column:stream_key;size:128;not null;uniqueIndex" json:"stream_key"`
	DirectionType string    `gorm:"column:direction_type;size:16;not null" json:"direction_type"`
	RTSPURL       string    `gorm:"column:rtsp_url;size:1024" json:"rtsp_url"`
	Status        string    `gorm:"column:status;size:16;not null" json:"status"`
	LastActiveAt  time.Time `gorm:"column:last_active_at" json:"last_active_at"`
	CreatedAt     time.Time `gorm:"column:created_at" json:"created_at"`
	UpdatedAt     time.Time `gorm:"column:updated_at" json:"updated_at"`
}

// TableName 显式指定表名，保证与 PRD 表结构一致。
func (Device) TableName() string { return "devices" }

// StreamSession 记录每一次转码轨道切换时的底层指标快照，
// 用于大屏回放与后续「双轨对比」数据分析。
type StreamSession struct {
	ID            uint      `gorm:"primaryKey" json:"id"`
	DeviceID      uint      `gorm:"column:device_id;index;not null" json:"device_id"`
	CurrentCodec  string    `gorm:"column:current_codec;size:32" json:"current_codec"`
	TranscodeMode string    `gorm:"column:transcode_mode;size:32" json:"transcode_mode"`
	BitrateKbps   int       `gorm:"column:bitrate_kbps" json:"bitrate_kbps"`
	FPS           int       `gorm:"column:fps" json:"fps"`
	LossRate      string    `gorm:"column:loss_rate;size:16" json:"loss_rate"`
	Resolution    string    `gorm:"column:resolution;size:16" json:"resolution"`
	CreatedAt     time.Time `gorm:"column:created_at" json:"created_at"`
}

// TableName 显式指定表名。
func (StreamSession) TableName() string { return "stream_sessions" }
