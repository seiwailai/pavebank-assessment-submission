param(
    [string]$Tags = $(if ($env:UAT_TAGS) { $env:UAT_TAGS } else { "~@manual" }),
    [string]$Features = $(if ($env:UAT_FEATURES) { $env:UAT_FEATURES } else { "features" }),
    [switch]$ReuseExisting
)

$scriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$feesDir = Resolve-Path (Join-Path $scriptDir "..\..")
Set-Location $feesDir

$args = @(
    "-v",
    "./uat",
    "-run", "TestUAT",
    "-count=1",
    "-args",
    "-uat-tags=$Tags",
    "-uat-features=$Features"
)

if (-not $ReuseExisting) {
    $args += "-uat-manage-runtime"
}

go test @args
