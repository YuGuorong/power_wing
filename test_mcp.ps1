# MCP API Test Script for PowerWing
param($ServerHost = "127.0.0.1", $ServerPort = 9876)

function Send-McpRequest($method, $params, $id = 1) {
    $body = @{ jsonrpc = "2.0"; id = $id; method = $method }
    if ($params) { $body.params = $params }
    $json = ConvertTo-Json -InputObject $body -Depth 10 -Compress
    $frame = "Content-Length: $($json.Length)`r`n`r`n$json"
    $bytes = [System.Text.Encoding]::UTF8.GetBytes($frame)
    
    $client = New-Object System.Net.Sockets.TcpClient($ServerHost, $ServerPort)
    $stream = $client.GetStream()
    $stream.Write($bytes, 0, $bytes.Length)
    $stream.Flush()
    
    # Read Content-Length header
    $reader = New-Object System.IO.StreamReader($stream)
    $headerLine = $reader.ReadLine()
    if ($headerLine -match "Content-Length: (\d+)") {
        $len = [int]$Matches[1]
        # Skip blank line
        $reader.ReadLine() | Out-Null
        # Read body
        $buf = New-Object char[] $len
        $reader.Read($buf, 0, $len) | Out-Null
        $resp = -join $buf
    } else {
        $resp = $reader.ReadToEnd()
    }
    
    $client.Close()
    return ConvertFrom-Json $resp
}

Write-Host "=== MCP API Test for PowerWing ===" -ForegroundColor Cyan
Write-Host "Server: ${ServerHost}:${ServerPort}`n"

# 1. Initialize
Write-Host "1. Initialize..." -ForegroundColor Yellow
$r = Send-McpRequest "initialize" @{ protocolVersion = "2024-11-05"; capabilities = @{}; clientInfo = @{ name = "test"; version = "1.0" } }
Write-Host "   Server: $($r.result.serverInfo.name) $($r.result.serverInfo.version)" -ForegroundColor Green

# Send initialized notification
$null = Send-McpRequest "notifications/initialized" $null 2

# 2. List tools
Write-Host "`n2. List tools..." -ForegroundColor Yellow
$r = Send-McpRequest "tools/list" $null 3
foreach ($t in $r.result.tools) {
    Write-Host "   - $($t.name): $($t.description)" -ForegroundColor Green
}

# 3. List devices
Write-Host "`n3. List devices..." -ForegroundColor Yellow
$r = Send-McpRequest "tools/call" @{ name = "powerwing_list_devices"; arguments = @{} } 4
$text = $r.result.content[0].text | ConvertFrom-Json
Write-Host ($text | ConvertTo-Json -Depth 5) -ForegroundColor White

# 4. Test connection for SPM3051
Write-Host "`n4. Test connection (spm3051)..." -ForegroundColor Yellow
$r = Send-McpRequest "tools/call" @{ name = "powerwing_test_connection"; arguments = @{ device_type = "spm3051" } } 5
$text = $r.result.content[0].text | ConvertFrom-Json
Write-Host ($text | ConvertTo-Json -Depth 5) -ForegroundColor White

# 5. Get config
Write-Host "`n5. Get config..." -ForegroundColor Yellow
$r = Send-McpRequest "tools/call" @{ name = "powerwing_get_config"; arguments = @{} } 6
$text = $r.result.content[0].text | ConvertFrom-Json
Write-Host ($text | ConvertTo-Json -Depth 5) -ForegroundColor White

# 6. Get device state for PSU1
Write-Host "`n6. Get device state (PSU1)..." -ForegroundColor Yellow
$r = Send-McpRequest "tools/call" @{ name = "powerwing_get_device_state"; arguments = @{ device_id = "PSU1" } } 7
$text = $r.result.content[0].text | ConvertFrom-Json
Write-Host ($text | ConvertTo-Json -Depth 5) -ForegroundColor White

# 7. List serial ports
Write-Host "`n7. List serial ports..." -ForegroundColor Yellow
$r = Send-McpRequest "tools/call" @{ name = "powerwing_list_serial_ports"; arguments = @{} } 8
$text = $r.result.content[0].text | ConvertFrom-Json
Write-Host ($text | ConvertTo-Json -Depth 5) -ForegroundColor White

Write-Host "`n=== Tests Complete ===" -ForegroundColor Cyan
