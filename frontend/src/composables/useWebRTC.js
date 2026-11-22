import { onBeforeUnmount, ref } from 'vue'

/**
 * useWebRTC：浏览器与 Nexus-Core 之间的非 Trickle（一次 Offer/Answer）协商。
 *
 * 服务端在返回 Answer 前会等待 ICE Gathering 完成（最长 6s），
 * 因此前端也需要在本端候选收集完成后再提交 Offer，双方 SDP 都是自包含的。
 */
export function useWebRTC(videoRef) {
  const state = ref('idle') // idle | connecting | playing | error
  const codec = ref('')
  const peers = ref(0)
  const error = ref('')
  const latencyHint = ref('')

  let pc = null
  let disposed = false

  function waitForIce(pc, timeout = 3000) {
    if (pc.iceGatheringState === 'complete') return Promise.resolve()
    return new Promise((resolve) => {
      let timer = null
      const finish = () => {
        pc.removeEventListener('icegatheringstatechange', onChange)
        if (timer) clearTimeout(timer)
        resolve()
      }
      const onChange = () => {
        if (pc.iceGatheringState === 'complete') finish()
      }
      pc.addEventListener('icegatheringstatechange', onChange)
      timer = setTimeout(finish, timeout)
    })
  }

  function attachStream(stream) {
    const video = videoRef.value
    if (!video) return
    video.srcObject = stream
    const played = video.play()
    if (played && typeof played.catch === 'function') {
      played.catch(() => {
        // 浏览器自动播放策略：等待用户点击后由 VideoStage 的遮罩触发 play()。
        state.value = 'idle'
        error.value = '浏览器拦截了自动播放，请点击画面继续'
      })
    }
  }

  async function stop() {
    if (pc) {
      pc.ontrack = null
      pc.onconnectionstatechange = null
      pc.close()
      pc = null
    }
    if (videoRef.value) {
      videoRef.value.srcObject = null
    }
  }

  async function connect() {
    await stop()
    state.value = 'connecting'
    error.value = ''

    try {
      const p = new RTCPeerConnection({ iceServers: [] })
      pc = p

      const stream = new MediaStream()
      p.ontrack = (event) => {
        event.streams[0]?.getTracks().forEach((track) => stream.addTrack(track))
        if (!event.streams.length && event.track) stream.addTrack(event.track)
        attachStream(stream)
      }

      p.onconnectionstatechange = () => {
        if (disposed || !pc) return
        switch (pc.connectionState) {
          case 'connected':
            state.value = 'playing'
            error.value = ''
            break
          case 'failed':
          case 'closed':
            state.value = 'error'
            error.value = 'WebRTC 连接中断，可点击重连'
            break
          default:
            break
        }
      }

      p.addTransceiver('video', { direction: 'recvonly' })
      p.addTransceiver('audio', { direction: 'recvonly' })

      const offer = await p.createOffer()
      await p.setLocalDescription(offer)
      await waitForIce(p)

      if (!pc) return

      const res = await fetch('/api/webrtc/offer', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ sdp: pc.localDescription.sdp, type: pc.localDescription.type }),
      })
      const payload = await res.json().catch(() => ({}))
      if (!res.ok) {
        throw new Error(payload.error || `协商失败 (${res.status})`)
      }

      codec.value = payload.codec || ''
      peers.value = payload.peers || 0

      await p.setRemoteDescription({ type: payload.type || 'answer', sdp: payload.sdp })

      const statsTimer = setInterval(async () => {
        if (!pc) {
          clearInterval(statsTimer)
          return
        }
        try {
          const report = await pc.getStats()
          report.forEach((item) => {
            if (item.type === 'inbound-rtp' && item.kind === 'video') {
              const jitter = item.jitter !== undefined ? Math.round(item.jitter * 1000) : 0
              latencyHint.value = jitter ? `抖动 ${jitter}ms` : ''
            }
          })
        } catch {
          /* getStats 失败不影响播放 */
        }
      }, 3000)
    } catch (err) {
      state.value = 'error'
      error.value = err instanceof Error ? err.message : String(err)
      await stop()
    }
  }

  function resumePlayback() {
    if (videoRef.value) videoRef.value.play().catch(() => {})
  }

  onBeforeUnmount(() => {
    disposed = true
    stop()
  })

  return { state, codec, peers, error, latencyHint, connect, stop, resumePlayback }
}
