# 🧪 MCP TTS Server - Test Suite

## 📋 **Test Files Overview**

### **🔧 Testing Scripts**
- **`basic_test.sh`** - Quick health check for server startup and basic functionality
- **`simple_test.sh`** - Tests cancellation functionality with OpenAI TTS  
- **`auto_test.sh`** - Automated comprehensive testing across all TTS engines

### **📊 MCP Protocol Tests** 
- **`initialize.json`** - MCP server initialization
- **`tools_list.json`** - List available tools
- **`prompts_list.json`** - List available prompts

### **🎤 TTS Engine Tests**
- **`say.json`** - Test macOS built-in say command
- **`elevenlabs.json`** - Test ElevenLabs TTS API
- **`google_tts.json`** - Test Google Gemini TTS API
- **`openai_tts.json`** - Basic OpenAI TTS test
- **`openai_tts_instructions.json`** - OpenAI TTS with custom voice instructions
- **`openai_tts_comprehensive.json`** - Full OpenAI TTS feature test
- **`tts_elicitation.jsonl`** - Interactive `tts` request fixture consumed by the Go integration harness; the harness supplies the `elicitation/create` replies, so this is not a standalone stdin transcript

### **🔍 Other Files**
- **`main.go`** - Go-based test runner
- **`hack.json`** - Security test for prompt injection prevention
- **`hack_say_tts_text.json`** - Security test for say_tts text argument injection
- **`hack_say_tts_voice.json`** - Security test for say_tts voice argument injection
- **`hack_say_tts_rate.jsonl`** - Security test for say_tts rate argument injection

## 🚀 **Quick Testing**

### Basic Health Check
```bash
./basic_test.sh
```

### Test Cancellation Feature  
```bash
./simple_test.sh
```

### Full Test Suite
```bash
./auto_test.sh
```

### Security Testing
```bash
# Test prompt injection prevention
cat hack.json | go run ../main.go

# Verify no files were created (prompt injection prevented)
ls /tmp/hacked* 2>/dev/null || echo "✅ Prompt injection prevented!"

# Test text argument injection via say_tts
cat hack_say_tts_text.json | go run ../main.go

# Verify no files were created (text injection prevented)
ls /tmp/hacked_say_tts_text* 2>/dev/null || echo "✅ Text injection prevented!"

# Test voice argument injection via say_tts
cat hack_say_tts_voice.json | go run ../main.go

# Verify no files were created (voice injection prevented)
ls /tmp/hacked_say_tts_voice* 2>/dev/null || echo "✅ Voice injection prevented!"

# Test rate argument injection via say_tts
cat hack_say_tts_rate.jsonl | go run ../main.go

# Verify no files were created (rate injection prevented)
ls /tmp/hacked_rate* 2>/dev/null || echo "✅ Rate injection prevented!"
```

### Manual MCP Testing
```bash
# Start server
go run ../main.go

# In another terminal, test individual features:
cat initialize.json | nc localhost 3000
cat tools_list.json | nc localhost 3000  
cat openai_tts.json | nc localhost 3000
```

## 📋 **Environment Setup**

Required API keys for full testing:
```bash
export OPENAI_API_KEY="your-openai-key"
export ELEVENLABS_API_KEY="your-elevenlabs-key"  
export GOOGLE_AI_API_KEY="your-google-key"
```

## ✅ **What Was Cleaned Up**

Removed in this cleanup:
- ❌ `cancel_*.jsonl` files (not usable with current MCP implementation)
- ❌ `long_*.jsonl` files (redundant test data)  
- ❌ 6 redundant shell scripts (kept only the best 3)
- ❌ 2 redundant documentation files (kept the comprehensive one)
- ❌ Outdated API test files

**Kept important files:**
- ✅ `hack.json` - Security test for command injection prevention
- ✅ Core functionality tests for all TTS engines
- ✅ MCP protocol validation tests

This focused test suite covers all essential functionality including security testing without redundancy.
