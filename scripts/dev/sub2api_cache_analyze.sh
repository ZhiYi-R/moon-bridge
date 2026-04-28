#!/usr/bin/env bash
set -euo pipefail

BASE_URL="${BASE_URL:?Please set BASE_URL environment variable}"
API_KEY="${API_KEY:?Please set API_KEY environment variable}"

curl -s -X GET "${BASE_URL%/}/v1/usage" \
  -H "Authorization: Bearer ${API_KEY}" |
jq -r '
.model_stats[] |
[
  .model,
  (.total_tokens // 0),
  (.input_tokens // 0),
  (.cache_read_tokens // 0),
  (.cache_creation_tokens // 0),
  (.output_tokens // 0),
  (.cost // 0),
  (if ((.input_tokens // 0) + (.cache_read_tokens // 0)) == 0
   then 0
   else ((.cache_read_tokens // 0) / ((.input_tokens // 0) + (.cache_read_tokens // 0)) * 100)
   end),
  (if (.cache_creation_tokens // 0) == 0
   then null
   else ((.cache_read_tokens // 0) / (.cache_creation_tokens // 0))
   end)
] | @tsv
' | awk 'BEGIN {
  OFS="\t";
  print "模型ID","总计Token数","输入Token数","缓存读取Token数","缓存写入Token数","输出Token数","模型费用","缓存命中率","缓存读写比"
}
{
  ratio = ($9 == "" ? "N/A" : sprintf("%.2f", $9));
  hit   = sprintf("%.2f%%", $8);
  cost  = sprintf("%.2f", $7);
  print $1,$2,$3,$4,$5,$6,cost,hit,ratio
}' | column -t -s $'\t'