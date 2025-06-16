package powershell

// SAPI PowerShell script for text-to-speech
const SAPIScript = "Add-Type -AssemblyName System.Speech\n" +
	"$synth = New-Object System.Speech.Synthesis.SpeechSynthesizer\n" +
	"\n" +
	"# Set voice if provided\n" +
	"if ($voice) {\n" +
	"    try {\n" +
	"        $synth.SelectVoice($voice)\n" +
	"    } catch {\n" +
	"        Write-Error \"Voice '$voice' not found. Available voices: $($synth.GetInstalledVoices().VoiceInfo.Name -join ', ')\"\n" +
	"        exit 1\n" +
	"    }\n" +
	"}\n" +
	"\n" +
	"# Set rate if provided (-10 to 10)\n" +
	"if ($rate -ne $null) {\n" +
	"    $synth.Rate = $rate\n" +
	"}\n" +
	"\n" +
	"# Speak the text\n" +
	"$synth.Speak($text)"

// WinRT PowerShell script for text-to-speech with SSML support
const WinRTScript = "Add-Type -AssemblyName System.Runtime.WindowsRuntime\n" +
	"Add-Type -AssemblyName System.Runtime.InteropServices.WindowsRuntime\n" +
	"\n" +
	"# Load WinRT Speech API\n" +
	"[Windows.Media.SpeechSynthesis.SpeechSynthesizer,Windows.Media.SpeechSynthesis,ContentType=WindowsRuntime] | Out-Null\n" +
	"[Windows.Storage.Streams.DataReader,Windows.Storage.Streams,ContentType=WindowsRuntime] | Out-Null\n" +
	"\n" +
	"# Helper for async operations\n" +
	"$AsTask = [System.WindowsRuntimeSystemExtensions].GetMethods() | Where-Object { \n" +
	"    $_.Name -eq 'AsTask' -and \n" +
	"    $_.GetParameters().Count -eq 1 -and \n" +
	"    $_.GetParameters()[0].ParameterType.Name -eq 'IAsyncOperation`1' \n" +
	"} | Select-Object -First 1\n" +
	"\n" +
	"function Wait-AsyncTask($WinRtTask, $ResultType) {\n" +
	"    $asTaskGeneric = $AsTask.MakeGenericMethod($ResultType)\n" +
	"    $netTask = $asTaskGeneric.Invoke($null, @($WinRtTask))\n" +
	"    $netTask.Wait() | Out-Null\n" +
	"    return $netTask.Result\n" +
	"}\n" +
	"\n" +
	"try {\n" +
	"    # Create synthesizer\n" +
	"    $synth = New-Object Windows.Media.SpeechSynthesis.SpeechSynthesizer\n" +
	"    \n" +
	"    # Set voice if provided\n" +
	"    if ($voice) {\n" +
	"        $voices = [Windows.Media.SpeechSynthesis.SpeechSynthesizer]::AllVoices\n" +
	"        $selectedVoice = $voices | Where-Object { $_.DisplayName -eq $voice }\n" +
	"        if (-not $selectedVoice) {\n" +
	"            Write-Error \"Voice '$voice' not found. Available voices: $($voices.DisplayName -join ', ')\"\n" +
	"            exit 1\n" +
	"        }\n" +
	"        $synth.Voice = $selectedVoice\n" +
	"    }\n" +
	"    \n" +
	"    # Determine final text and synthesis method\n" +
	"    $finalText = $text\n" +
	"    $useSSML = $false\n" +
	"    \n" +
	"    # Check if input is already SSML\n" +
	"    if ($isSSML) {\n" +
	"        $finalText = $text\n" +
	"        $useSSML = $true\n" +
	"    } elseif ($rate -ne $null) {\n" +
	"        # Apply rate via SSML if provided\n" +
	"        # Convert rate (-10 to 10) to SSML named values or percentages\n" +
	"        switch ($rate) {\n" +
	"            -10 { $rateValue = 'x-slow' }\n" +
	"            -5 { $rateValue = 'slow' }\n" +
	"            0 { $rateValue = 'medium' }\n" +
	"            5 { $rateValue = 'fast' }\n" +
	"            10 { $rateValue = 'x-fast' }\n" +
	"            default {\n" +
	"                # For other values, use percentage (0.5x to 2.0x)\n" +
	"                $multiplier = 1.0 + ($rate * 0.05)\n" +
	"                $rateValue = [string]$multiplier\n" +
	"            }\n" +
	"        }\n" +
	"        $finalText = \"<speak version='1.0' xmlns='http://www.w3.org/2001/10/synthesis' xml:lang='en-US'><prosody rate='$rateValue'>$([System.Security.SecurityElement]::Escape($text))</prosody></speak>\"\n" +
	"        $useSSML = $true\n" +
	"    }\n" +
	"    \n" +
	"    # Synthesize to stream\n" +
	"    if ($useSSML) {\n" +
	"        # Use SSML synthesis\n" +
	"        $streamTask = $synth.SynthesizeSsmlToStreamAsync($finalText)\n" +
	"    } else {\n" +
	"        # Use regular text synthesis\n" +
	"        $streamTask = $synth.SynthesizeTextToStreamAsync($finalText)\n" +
	"    }\n" +
	"    $stream = Wait-AsyncTask $streamTask ([Windows.Media.SpeechSynthesis.SpeechSynthesisStream])\n" +
	"    \n" +
	"    # Read stream to bytes\n" +
	"    $reader = New-Object Windows.Storage.Streams.DataReader($stream)\n" +
	"    $loadTask = $reader.LoadAsync([uint32]$stream.Size)\n" +
	"    $bytesLoaded = Wait-AsyncTask $loadTask ([uint32])\n" +
	"    $buffer = New-Object byte[] $bytesLoaded\n" +
	"    $reader.ReadBytes($buffer)\n" +
	"    \n" +
	"    # Save and play audio\n" +
	"    $tempFile = [System.IO.Path]::GetTempFileName().Replace('.tmp', '.wav')\n" +
	"    [System.IO.File]::WriteAllBytes($tempFile, $buffer)\n" +
	"    \n" +
	"    Add-Type -AssemblyName System.Windows.Forms\n" +
	"    $player = New-Object System.Media.SoundPlayer($tempFile)\n" +
	"    $player.PlaySync()\n" +
	"    \n" +
	"    # Cleanup\n" +
	"    $player.Dispose()\n" +
	"    $reader.Dispose()\n" +
	"    $stream.Dispose()\n" +
	"    Remove-Item $tempFile -ErrorAction SilentlyContinue\n" +
	"\n" +
	"} catch {\n" +
	"    Write-Error \"WinRT TTS Error: $_\"\n" +
	"    exit 1\n" +
	"}"

// Voice enumeration script for both APIs
const VoiceEnumerationScript = "$voices = @()\n" +
	"\n" +
	"# Get SAPI voices\n" +
	"try {\n" +
	"    Add-Type -AssemblyName System.Speech\n" +
	"    $sapi = New-Object System.Speech.Synthesis.SpeechSynthesizer\n" +
	"    $sapiVoices = $sapi.GetInstalledVoices() | ForEach-Object {\n" +
	"        [PSCustomObject]@{\n" +
	"            Name = $_.VoiceInfo.Name\n" +
	"            Language = $_.VoiceInfo.Culture.Name\n" +
	"            Gender = $_.VoiceInfo.Gender.ToString()\n" +
	"            API = \"SAPI\"\n" +
	"        }\n" +
	"    }\n" +
	"    $voices += $sapiVoices\n" +
	"} catch {\n" +
	"    Write-Warning \"Failed to enumerate SAPI voices: $_\"\n" +
	"}\n" +
	"\n" +
	"# Get WinRT voices\n" +
	"try {\n" +
	"    Add-Type -AssemblyName System.Runtime.WindowsRuntime\n" +
	"    [Windows.Media.SpeechSynthesis.SpeechSynthesizer,Windows.Media.SpeechSynthesis,ContentType=WindowsRuntime] | Out-Null\n" +
	"    $winrtVoices = [Windows.Media.SpeechSynthesis.SpeechSynthesizer]::AllVoices | ForEach-Object {\n" +
	"        [PSCustomObject]@{\n" +
	"            Name = $_.DisplayName\n" +
	"            Language = $_.Language\n" +
	"            Gender = $_.Gender.ToString()\n" +
	"            API = \"WinRT\"\n" +
	"        }\n" +
	"    }\n" +
	"    $voices += $winrtVoices\n" +
	"} catch {\n" +
	"    Write-Warning \"Failed to enumerate WinRT voices: $_\"\n" +
	"}\n" +
	"\n" +
	"# Output results as JSON for easy parsing\n" +
	"$voices | ConvertTo-Json -Depth 2"