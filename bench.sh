#!/bin/bash

MAX_RETRIES=100000
if [[ "$OSTYPE" == "darwin"* ]] && ! command -v gdate >/dev/null 2>&1; then
  echo "bench.sh needs gdate (coreutils) on macOS: brew install coreutils" >&2
  exit 1
fi

# High-resolution epoch seconds. macOS date(1) lacks %N; coreutils gdate
# provides it. Command substitution runs in a subshell, so no state escapes.
now() {
  if [[ "$OSTYPE" == "linux-gnu"* ]]; then
    date +%s.%N
  else
    gdate +%s.%N
  fi
}

# 7*42 + 48
START=$(now)
for ((i = 1; i <= MAX_RETRIES; i++)); do
  echo -e '\r';
  echo -e '\033[0K\033[1mBold\033[0m \033[7mInvert\033[0m \033[4mUnderline\033[0m';
  echo -e '\033[0K\033[1m\033[7m\033[4mBold & Invert & Underline\033[0m';
  echo;
  echo -e '\033[0K\033[31m Red \033[32m Green \033[33m Yellow \033[34m Blue \033[35m Magenta \033[36m Cyan \033[0m';
  echo -e '\033[0K\033[1m\033[4m\033[31m Red \033[32m Green \033[33m Yellow \033[34m Blue \033[35m Magenta \033[36m Cyan \033[0m';
  echo;
  echo -e '\033[0K\033[41m Red \033[42m Green \033[43m Yellow \033[44m Blue \033[45m Magenta \033[46m Cyan \033[0m';
  echo -e '\033[0K\033[1m\033[4m\033[41m Red \033[42m Green \033[43m Yellow \033[44m Blue \033[45m Magenta \033[46m Cyan \033[0m';
  echo;
  echo -e '\033[0K\033[30m\033[41m Red \033[42m Green \033[43m Yellow \033[44m Blue \033[45m Magenta \033[46m Cyan \033[0m';
  echo -e '\033[0K\033[30m\033[1m\033[4m\033[41m Red \033[42m Green \033[43m Yellow \033[44m Blue \033[45m Magenta \033[46m Cyan \033[0m';
done
END=$(now)
echo "Coloured output test takes: " $(echo "($END - $START)" | bc) " seconds"
COLOURED_OUPUT=$(echo "(300 * $MAX_RETRIES) / ($END - $START)" | bc)

START=$(now)
for ((i = 1; i <= MAX_RETRIES; i++)); do
  echo -e '\r';
  echo -e '🎫💋📂💣💒💁💀💳📄📕📦📷🔈🔙🔪🔻🔻🕊🕊🕛🕬🕽🖎🖎🖎🖍🖞🗀🗑🗢🗳🗡🗤🗣🗺🗻🗼🗽🗾🗿🗮🗝🗌🖻🖪🖙🖈🕷🕦🕕🔳🔢🔑🔀📯📞📍💼💫💚💉👸👧👖🐴🐣🐒🐁🏰🏟🏎🎽🎬🎛🎊🍹🍨🍗';
done
END=$(now)
echo "Unicode output test takes: " $(echo "($END - $START)" | bc) " seconds"
UNICODE_OUPUT=$(echo "(139 * $MAX_RETRIES) / ($END - $START)" | bc)


START=$(now)
for ((i = 1; i <= MAX_RETRIES; i++)); do
  echo -e '\r';
  echo -e 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZ';
done
END=$(now)
echo "Non-unicode output test takes: " $(echo "($END - $START)" | bc) " seconds"
NONE_UNICODE_OUPUT=$(echo "(118 * $MAX_RETRIES) / ($END - $START)" | bc)

test_output='';
START=$(now)
for x in {1..10}; do
  test_output="${test_output} a🎫"
  for ((i = 1; i <= MAX_RETRIES; i++)); do
    echo -e '\r';
    echo -e "$test_output";
  done
done
END=$(now)
echo "Mixed output test takes: " $(echo "($END - $START)" | bc) " seconds"
MIXED_OUPUT=$(echo "(165 * $MAX_RETRIES) / ($END - $START)" | bc)


echo "${COLOURED_OUPUT} coloured characters per second"
echo "${UNICODE_OUPUT} unicode characters per second"
echo "${NONE_UNICODE_OUPUT} none-unicode characters per second"
echo "${MIXED_OUPUT} Mixed characters per second"
