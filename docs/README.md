# 📚 MCP TTS Server Documentation

## 🎯 **Quick Start**
- **[Main README](../README.md)** - Project overview, installation, and usage
- **[Test Suite](../test/README.md)** - Testing guide and test files

## 🔒 **Security**
- **[Security Review](security/SECURITY_REVIEW.md)** - Comprehensive security analysis and fixes
- **[Command Injection Testing](../test/README.md#security-testing)** - How to test security features

## 🛠️ **Implementation**
- **[Cancellation System](implementation/cancellation.md)** - Complete cancellation feature implementation
- **[Architecture Overview](#architecture)** - System design and components

## 🧪 **Testing**
- **[Google TTS Tests](testing/google-tts-tests.md)** - Detailed Google TTS test documentation
- **[Test Suite Guide](../test/README.md)** - How to run and use tests

## 🏗️ **Architecture**

### **Core Components**
- **`cmd/root.go`** - Main MCP server with 4 TTS tools (say, ElevenLabs, Google, OpenAI)
- **`cmd/cancellation_manager.go`** - Request tracking and cancellation system
- **`cmd/cancellable_wrapper.go`** - Tool wrapper providing cancellation support
- **`cmd/notification_handler.go`** - MCP cancellation notification processing

### **TTS Tools**
1. **`say_tts`** - macOS built-in text-to-speech (macOS only)
2. **`elevenlabs_tts`** - ElevenLabs API integration
3. **`google_tts`** - Google Gemini TTS models
4. **`openai_tts`** - OpenAI TTS API integration

### **Security Features**
- ✅ Request ID sanitization and validation
- ✅ Resource limits (1000 concurrent requests max)
- ✅ Input validation for all user inputs
- ✅ Command injection prevention
- ✅ Memory leak prevention with automatic cleanup

## 📊 **API Documentation**

### **Environment Variables**
```bash
# Required for respective services
export OPENAI_API_KEY="your-openai-key"
export ELEVENLABS_API_KEY="your-elevenlabs-key"  
export GOOGLE_AI_API_KEY="your-google-key"

# Optional configuration
export OPENAI_TTS_INSTRUCTIONS="Custom voice instructions"
export ELEVENLABS_VOICE_ID="custom-voice-id"
export ELEVENLABS_MODEL_ID="custom-model-id"
```

### **MCP Tools**
All tools support cancellation via MCP `notifications/cancelled` protocol.

#### **say_tts** (macOS only)
```json
{
  "name": "say_tts",
  "arguments": {
    "text": "Text to speak",
    "rate": 200,
    "voice": "Alex"
  }
}
```

#### **elevenlabs_tts**
```json
{
  "name": "elevenlabs_tts", 
  "arguments": {
    "text": "Text to speak"
  }
}
```

#### **google_tts**
```json
{
  "name": "google_tts",
  "arguments": {
    "text": "Text to speak",
    "voice": "Kore",
    "model": "gemini-2.5-flash-preview-tts"
  }
}
```

#### **openai_tts**
```json
{
  "name": "openai_tts",
  "arguments": {
    "text": "Text to speak",
    "voice": "coral",
    "model": "gpt-4o-mini-tts",
    "speed": 1.0,
    "instructions": "Speak clearly and professionally"
  }
}
```

## 🚀 **Deployment**

### **Production Checklist**
- [ ] Set required API keys as environment variables
- [ ] Run with non-root user
- [ ] Set up log rotation
- [ ] Configure resource limits
- [ ] Monitor for security warnings
- [ ] Use process supervisor (systemd/docker)

### **Performance**
- **Memory**: Bounded usage with 1000 request limit
- **Cancellation**: Immediate with context cancellation
- **Cleanup**: Automatic 30-minute timeout
- **Throughput**: Concurrent request processing

---

**Generated**: January 2025  
**Last Updated**: Security review and cancellation implementation complete 