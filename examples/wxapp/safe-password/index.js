// Integration fragment, not a standalone Mini Program. The business app supplies
// safePasswordAPI.issue()/verify() through its authenticated request layer and
// safePasswordUI { certPath, passwordLength }. Never ship a private key here.
Page({
  data: {
    ready: false,
    encryptionReady: false,
    inputGeneration: 0,
    loading: false,
    submitting: false,
    challenge: null,
    certPath: '',
    passwordLength: 0,
    inputValue: '',
    message: ''
  },

  onLoad() {
    this._active = true
    this._sequence = 0
    const app = getApp()
    this._api = app.safePasswordAPI
    const ui = app.safePasswordUI || {}
    if (!this._api || !this._api.issue || !this._api.verify || !ui.certPath || !ui.passwordLength) {
      this.setData({ message: '请先完成安全键盘接入配置' })
      return
    }
    if (!wx.canIUse('input.type.safe-password')) {
      this.setData({ message: '当前客户端不支持安全键盘，请升级或使用业务提供的其他验证方式' })
      return
    }
    this.setData({ certPath: ui.certPath, passwordLength: ui.passwordLength })
    this.loadChallenge()
  },

  onUnload() {
    this._active = false
    this._sequence += 1
    this._encryptedValue = ''
  },

  async loadChallenge() {
    if (!this._active || this.data.loading || this.data.submitting) return
    const sequence = ++this._sequence
    this._encryptedValue = ''
    this._lastEditTime = 0
    this.setData({ encryptionReady: false, loading: true, ready: false, inputValue: '', challenge: null })
    try {
      // Server resolves session, purpose, credential and hash profile itself.
      const challenge = await this._api.issue()
      if (!this._active || sequence !== this._sequence) return
      if (!challenge || !challenge.nonce || !challenge.timeStamp || !challenge.expiresAt) {
        throw new Error('invalid challenge')
      }
      this.setData({ challenge, ready: true })
    } catch (_) {
      if (this._active && sequence === this._sequence) {
        this.setData({ message: '暂时无法准备安全输入，请重试' })
      }
    } finally {
      if (this._active && sequence === this._sequence) this.setData({ loading: false })
    }
  },

  onEdited(event) {
    if (!this.data.ready || this.data.submitting) return
    // Do not inspect detail.value: only invalidate the previous encrypted input.
    this._encryptedValue = ''
    this._lastEditTime = event.timeStamp
    this.setData({ encryptionReady: false, inputGeneration: this.data.inputGeneration + 1 })
  },

  onEncrypted(event) {
    if (!this.data.ready || this.data.submitting || !this.data.challenge ||
        event.currentTarget.dataset.nonce !== this.data.challenge.nonce ||
        event.currentTarget.dataset.generation !== this.data.inputGeneration ||
        event.timeStamp < this._lastEditTime) return
    const detail = event.detail || {}
    this._encryptedValue = ''
    this.setData({ encryptionReady: false })
    if (detail.encryptError || !detail.encryptedValue) {
      this.setData({ message: '安全输入未完成，请重新输入' })
      return
    }
    // Keep ciphertext out of reactive state and logs. Ignore detail.value.
    this._encryptedValue = detail.encryptedValue
    this.setData({ encryptionReady: true, message: '' })
  },

  async submit() {
    if (this.data.submitting || !this.data.ready || !this.data.encryptionReady || !this._encryptedValue) return
    const sequence = this._sequence
    const payload = { nonce: this.data.challenge.nonce, encryptedValue: this._encryptedValue }
    this.setData({ submitting: true, message: '' })
    try {
      // This endpoint must validate/decode/consume the challenge AND perform the
      // business credential check. Decryption success alone is not login success.
      const result = await this._api.verify(payload)
      if (!result || result.verified !== true) throw new Error('not verified')
      if (this._active && sequence === this._sequence) this.setData({ message: '验证成功' })
    } catch (_) {
      if (this._active && sequence === this._sequence) {
        this.setData({ message: '验证未通过，请重新输入' })
      }
    } finally {
      this._encryptedValue = ''
      if (this._active && sequence === this._sequence) {
        this.setData({ submitting: false, ready: false, encryptionReady: false, inputValue: '' })
        await this.loadChallenge() // Fresh nonce after every submitted attempt.
      }
    }
  }
})
