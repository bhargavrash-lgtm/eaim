// background.js -- Manifest V3 service worker. Buffers paste events
// durably (chrome.storage.local, survives service-worker restarts) and
// flushes them to B0's native messaging host (eami-agent) on a timer or
// a count threshold, mirroring eami-collector's own proven
// buffer-then-forward pattern (SQLite buffer + periodic forwarder) --
// just backed by chrome.storage.local instead of SQLite, since that's
// the durable storage a browser extension actually has.
//
// This service worker never talks to the network directly and never
// talks to eami-api -- only to B0's native messaging host, by name.
importScripts("domains.js");

const STORAGE_KEY = "eami_paste_buffer";
const HOST_NAME = "com.eami.agent"; // must match B0's nmregister.HostName exactly
const FLUSH_ALARM_NAME = "eami-flush";
const COUNT_THRESHOLD = 10;
const FLUSH_PERIOD_MINUTES = 1; // chrome.alarms' documented minimum period for packed extensions
const SEND_TIMEOUT_MS = 10000;

// ── Alarm setup -- chrome.alarms (not setInterval, which does not survive
// service-worker suspension in Manifest V3) is the correct mechanism for
// a periodic wake-up here. Alarms are persisted by the browser itself,
// not by this script's in-memory state, so they keep firing across
// service-worker restarts with no extra bookkeeping needed.
chrome.runtime.onInstalled.addListener(ensureFlushAlarm);
chrome.runtime.onStartup.addListener(ensureFlushAlarm);
ensureFlushAlarm(); // also runs whenever this script itself is (re)evaluated after an SW restart

function ensureFlushAlarm() {
  chrome.alarms.get(FLUSH_ALARM_NAME, function (existing) {
    if (!existing) {
      chrome.alarms.create(FLUSH_ALARM_NAME, { periodInMinutes: FLUSH_PERIOD_MINUTES });
    }
  });
}

chrome.alarms.onAlarm.addListener(function (alarm) {
  if (alarm.name === FLUSH_ALARM_NAME) {
    flush();
  }
});

// ── Receiving events from content scripts ───────────────────────────────
chrome.runtime.onMessage.addListener(function (message) {
  if (!message || message.type !== "eami_paste_event") {
    return; // ignore anything unexpected -- not this extension's concern
  }
  // Defense in depth: never buffer/forward an event for a domain this
  // background script doesn't itself recognize, even though Chrome's own
  // content_scripts.matches should make that structurally impossible.
  // Never trust the sender's own classification blindly (same principle
  // as B-032/B0's server-side allowlist checks).
  if (!eamiIsKnownDomain(message.destination_domain)) {
    return;
  }
  bufferEvent({
    destination_domain: message.destination_domain,
    occurred_at: message.occurred_at,
    content_length: message.content_length,
    content_hash: message.content_hash,
  });
});

// ── Storage helpers + serialization ──────────────────────────────────────
//
// chrome.storage has no atomic read-modify-write primitive. A service
// worker is single-threaded, but that only rules out true parallelism --
// it does NOT serialize two independent get()...set() cycles whose
// callbacks are both still pending (e.g. two paste events arriving
// milliseconds apart from different tabs): both callbacks can read the
// same pre-update buffer, and whichever set() resolves second silently
// clobbers the other's append. A code-review pass caught this as a real,
// if narrow, contradiction of this file's own "never lose a
// buffered-but-unsent event" goal. runExclusive fixes it by serializing
// every storage read-modify-write critical section through a single
// promise chain -- callers queue behind whatever's already running,
// rather than racing it.
let storageQueue = Promise.resolve();

function runExclusive(criticalSection) {
  const result = storageQueue.then(criticalSection, criticalSection);
  // Swallow so one rejected critical section doesn't wedge the queue for
  // every operation queued after it; each caller still sees its own
  // rejection via the returned promise.
  storageQueue = result.then(function () {}, function () {});
  return result;
}

function storageGet(defaults) {
  return new Promise(function (resolve) {
    chrome.storage.local.get(defaults, function (result) {
      if (chrome.runtime.lastError) {
        console.error("eami: storage.local.get failed:", chrome.runtime.lastError.message);
      }
      resolve(result);
    });
  });
}

function storageSet(items) {
  return new Promise(function (resolve) {
    chrome.storage.local.set(items, function () {
      if (chrome.runtime.lastError) {
        console.error("eami: storage.local.set failed:", chrome.runtime.lastError.message);
      }
      resolve();
    });
  });
}

function bufferEvent(event) {
  runExclusive(function () {
    return storageGet({ [STORAGE_KEY]: [] }).then(function (result) {
      const buffer = result[STORAGE_KEY];
      buffer.push(event);
      // Written to durable storage BEFORE any send attempt -- this is what
      // makes buffered-but-unsent events survive a service-worker restart
      // (chrome.storage.local persists independently of the service
      // worker's lifecycle; only extension removal or an explicit
      // storage.local.clear() would lose it).
      return storageSet({ [STORAGE_KEY]: buffer }).then(function () {
        return buffer.length;
      });
    });
  }).then(function (newLength) {
    // Deliberately outside the lock: flush() acquires runExclusive itself
    // for its own critical sections, and calling it from inside one would
    // deadlock (the queued flush() operation would wait forever for a
    // lock this very call is still holding).
    if (newLength >= COUNT_THRESHOLD) {
      flush();
    }
  });
}

// ── Flushing to B0's native messaging host ──────────────────────────────
let flushInProgress = false;

function flush() {
  if (flushInProgress) {
    return; // avoid overlapping flushes if the alarm fires mid-flush
  }
  flushInProgress = true;

  runExclusive(function () {
    return storageGet({ [STORAGE_KEY]: [] });
  }).then(function (result) {
    const buffer = result[STORAGE_KEY];
    if (buffer.length === 0) {
      flushInProgress = false;
      return;
    }
    // sendBatch's native-messaging round trip runs OUTSIDE the lock
    // (it can take up to SEND_TIMEOUT_MS) -- only the storage
    // read-modify-write steps before and after it need to be serialized
    // against concurrent bufferEvent calls, not the slow part in between.
    sendBatch(buffer, function (sentCount) {
      runExclusive(function () {
        // Re-read fresh rather than assuming `buffer` is still current --
        // more events may have been buffered (via their own, separately
        // serialized runExclusive calls) while sendBatch's native-messaging
        // round trip was in flight. Only drop the leading `sentCount`
        // events (the ones actually acknowledged, in order); anything
        // else, including newly-arrived events, stays for the next
        // attempt.
        return storageGet({ [STORAGE_KEY]: [] }).then(function (latest) {
          const remaining = latest[STORAGE_KEY].slice(sentCount);
          return storageSet({ [STORAGE_KEY]: remaining });
        });
      }).then(function () {
        flushInProgress = false;
      }, function () {
        flushInProgress = false; // don't get stuck even if the re-read/write itself failed
      });
    });
  }, function () {
    flushInProgress = false; // don't get stuck if the initial read failed
  });
}

// sendBatch opens ONE native messaging connection per flush and sends
// every buffered event down it in sequence -- not one host-process spawn
// per event, which chrome.runtime.sendNativeMessage's one-shot
// connect+send+disconnect would do if called per event, an unnecessary
// process-spawn cost per paste when a whole batch can share one
// connection instead.
function sendBatch(events, done) {
  let port;
  try {
    port = chrome.runtime.connectNative(HOST_NAME);
  } catch (err) {
    done(0); // couldn't even start -- nothing sent, retried next cycle
    return;
  }

  let acked = 0;
  let settled = false;

  const finish = function (count) {
    if (settled) {
      return;
    }
    settled = true;
    clearTimeout(timeoutHandle);
    try {
      port.disconnect();
    } catch (err) {
      // already disconnected -- fine
    }
    done(count);
  };

  // If the host isn't registered/reachable (e.g. B0 not installed, or
  // this extension's ID isn't yet in B0's allowed_origins), Chrome
  // disconnects immediately and sets chrome.runtime.lastError. This is
  // deliberately never thrown/surfaced as a hard failure -- buffering and
  // silently retrying next cycle is the correct production behavior
  // (mirrors eami-collector's own forwarder, which logs transient send
  // failures at WARN and keeps retrying, never crashing or alerting a
  // user over a single failed attempt). But "silent" should mean "not a
  // hard failure", not "invisible" -- a real gap found during manual
  // testing: with nothing logged anywhere, there was no way to tell a
  // clean no-op flush (empty buffer) apart from a failed connection
  // attempt (host not registered) just by watching the console. Logging
  // here doesn't change the retry semantics at all, just makes the
  // reason visible for anyone actually looking (e.g. this extension's
  // own service-worker DevTools console during testing).
  port.onDisconnect.addListener(function () {
    if (chrome.runtime.lastError) {
      console.warn("eami: native messaging connection failed:", chrome.runtime.lastError.message);
    }
    finish(acked);
  });

  port.onMessage.addListener(function (response) {
    // Events are sent strictly in order, and B0's RunHost responds to
    // each in the order received (a single native-messaging connection
    // is a strict one-message-in, one-response-out stream) -- so the Nth
    // response corresponds to the Nth event sent. A single "error"
    // response stops counting further acks: safer to re-send a
    // possibly-already-delivered event next cycle than to drop one that
    // actually failed.
    if (response && response.status === "ok") {
      acked++;
      if (acked === events.length) {
        finish(acked);
      }
    } else {
      finish(acked);
    }
  });

  for (const event of events) {
    // This object's shape must match B0's nativemsg.PasteEventMessage
    // exactly: type, destination_domain, occurred_at (RFC3339 --
    // toISOString() already produces that), content_length, content_hash.
    // No org_id/agent_id/hostname -- B0 attaches its own known identity.
    port.postMessage({
      type: "paste_event",
      destination_domain: event.destination_domain,
      occurred_at: event.occurred_at,
      content_length: event.content_length,
      content_hash: event.content_hash,
    });
  }

  // Safety timeout: if the host never responds (hung process, crashed
  // mid-batch), don't leave flushInProgress stuck forever -- whatever
  // wasn't acknowledged stays buffered for the next attempt.
  const timeoutHandle = setTimeout(function () {
    finish(acked);
  }, SEND_TIMEOUT_MS);
}
