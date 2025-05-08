#!/bin/bash

# Input log file
LOG_FILE=$1
OUTPUT_FILE=$2

if [[ -z "$LOG_FILE" || -z "$OUTPUT_FILE" ]]; then
    echo "Usage: $0 <log_file> <output_file>"
    exit 1
fi

# Extract timestamp and offset
cat "$LOG_FILE"| grep "ptp4l\["|awk '{
    if ($1 ~ /^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}\.[0-9]+\+[0-9]{2}:[0-9]{2}$/) {
        timestamp = $1;
    }
    for (i=1; i<=NF; i++) {
        if ($i == "offset") {
            offset = $(i+1);
            print timestamp "," offset;
        }
    }
}' > "$OUTPUT_FILE"

echo "Extracted data saved to: $OUTPUT_FILE"
