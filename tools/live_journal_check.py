import sys
import time
import urllib.request

from llmtop.config import default_config
from llmtop.gateway import JournalIPSource

config = default_config()
source = JournalIPSource(config.gateway)
source.start()
deadline = time.time() + 20
while source.status == "starting" and time.time() < deadline:
    time.sleep(0.2)
print("journal status after start:", source.status)

request = urllib.request.Request(
    "http://10.0.0.6:4002/v1/models",
    headers={"User-Agent": "llmtop-journal-test/1", "x-request-id": "llmtop-journal-test-1"},
)
with urllib.request.urlopen(request, timeout=5) as response:
    response.read()

for _ in range(50):
    entry = source.lookup("llmtop-journal-test-1")
    if entry is not None:
        print("journal entry:", entry)
        source.stop()
        sys.exit(0)
    time.sleep(0.2)
print("no journal entry found; status:", source.status)
source.stop()
sys.exit(1)
