-- SPDX-License-Identifier: AGPL-3.0-or-later
-- 21 Remote Bridge: connects the JS plug-ins with the 21 Remote app.
--
-- A JS plug-in cannot open a connection to another program. This script runs
-- in the background (the installer starts it from Scripts/__startup.lua),
-- shares memory with the plug-ins ("Remote21") and exchanges files with the
-- app in ~/Library/Application Support/21 Remote/bridge. It also reports the
-- names of the tracks that carry "21 Channel" (for "Take ch. names from DAW")
-- and starts the app in the background when a plug-in needs it.

local NS = "Remote21"
local MAGIC = 21021
-- shared memory layout (see remote21.jsfx-inc)
local G_MAGIC, G_BEAT, G_LAYOUT, G_APP, G_ONLINE, G_NCH, G_RACK, G_WANT, G_IDS = 0, 1, 2, 3, 4, 5, 6, 7, 8
local G_CH, G_CHSIZE, G_CARD, G_CARDF, G_VAL, G_STR, G_STREND = 100, 10, 1100, 1120, 2000, 4000, 60000
local G_MBOX, G_MBSIZE, G_MBQ, G_CTEXT, G_CTSIZE, NINST = 61000, 300, 64, 81000, 1100, 64

local home = os.getenv("HOME") or ""
local dir = home .. "/Library/Application Support/21 Remote/bridge/"
local appDir = home .. "/Library/Application Support/21 Remote/"

local r = reaper
r.gmem_attach(NS)

-- one bridge per REAPER: a second copy (started by hand) stops at once
local token = tostring(r.time_precise()) .. tostring(math.random())
local other = r.GetExtState("Remote21", "bridge")
local otherT = tonumber(r.GetExtState("Remote21", "bridgeT")) or 0
if other ~= "" and other ~= token and r.time_precise() - otherT < 2 then return end

os.execute('mkdir -p "' .. dir .. '"')

local function unhex(s)
  if s == "-" then return "" end
  return (s:gsub("..", function(h) return string.char(tonumber(h, 16)) end))
end
local function tohex(s)
  if s == "" then return "-" end
  return (s:gsub(".", function(c) return string.format("%02x", c:byte()) end))
end

local function readFile(p)
  local f = io.open(p, "rb")
  if not f then return nil end
  local s = f:read("a")
  f:close()
  return s
end

local function append(lines)
  if #lines == 0 then return end
  local f = io.open(dir .. "commands.txt", "ab")
  if not f then return end
  f:write(table.concat(lines, "\n"), "\n")
  f:close()
end

local beat, lastSeq, seqT = 0, nil, 0
local lastLayout = nil
local layoutGen = 0
local appVals = {}          -- last values the app reported, key slot*128+cc
local lastClientBeat = -10
local lastWant, wantT = nil, -100
local lastLaunch = -100
local lastTracks, tracksT = nil, -10

-- the layout and values of state.txt to shared memory
local function applyState(s)
  local layout = {}
  local vals = {}
  for line in s:gmatch("[^\n]+") do
    local k = line:match("^(%S+)")
    if k == "val" then
      local slot, cc, v = line:match("^val (%d+) (%d+) (%d+)")
      if slot then vals[(tonumber(slot) - 1) * 128 + tonumber(cc)] = tonumber(v) end
    elseif k ~= "seq" and k ~= "R21" then
      layout[#layout + 1] = line
    end
  end
  local sig = table.concat(layout, "\n")
  if sig ~= lastLayout then
    lastLayout = sig
    local nch, str = 0, G_STR
    local function putStr(t)
      if str + #t + 1 >= G_STREND then t = "" end
      local at = str
      r.gmem_write(at, #t)
      for i = 1, #t do r.gmem_write(at + i, t:byte(i)) end
      str = str + #t + 1
      return at
    end
    r.gmem_write(G_ONLINE, 0)
    for _, line in ipairs(layout) do
      local k = line:match("^(%S+)")
      if k == "online" then
        r.gmem_write(G_ONLINE, tonumber(line:match("(%d+)$")) or 0)
      elseif k == "rack" then
        r.gmem_write(G_RACK, putStr(unhex(line:match("^rack (%S+)") or "-")))
      elseif k == "card" then
        local slot, t, f = line:match("^card (%d+) (%d+) (%d+)")
        if slot then
          r.gmem_write(G_CARD + tonumber(slot) - 1, tonumber(t))
          r.gmem_write(G_CARDF + tonumber(slot) - 1, tonumber(f))
        end
      elseif k == "ch" and nch < 96 then
        local slot, ch, kind, linked, col, name, label, comment, ids =
          line:match("^ch (%d+) (%d+) (%d+) (%d+) (%d+) (%S+) (%S+) (%S+) (%S+)")
        if slot then
          local b = G_CH + nch * G_CHSIZE
          r.gmem_write(b, tonumber(slot)); r.gmem_write(b + 1, tonumber(ch))
          r.gmem_write(b + 2, tonumber(kind)); r.gmem_write(b + 3, tonumber(linked))
          r.gmem_write(b + 4, tonumber(col))
          r.gmem_write(b + 5, putStr(unhex(name))); r.gmem_write(b + 6, putStr(unhex(label)))
          r.gmem_write(b + 7, putStr(unhex(comment))); r.gmem_write(b + 8, putStr(unhex(ids)))
          nch = nch + 1
        end
      end
    end
    r.gmem_write(G_NCH, nch)
    layoutGen = layoutGen + 1
    r.gmem_write(G_LAYOUT, layoutGen)
  end
  -- values: only what the app changed, so a plug-in's own fresh value stays
  for key, v in pairs(vals) do
    if appVals[key] ~= v then r.gmem_write(G_VAL + key, v) end
  end
  for key in pairs(appVals) do
    if vals[key] == nil then r.gmem_write(G_VAL + key, -1) end
  end
  appVals = vals
end

-- commands the plug-ins left in their mailboxes
local function readMailboxes()
  local out = {}
  for id = 0, NINST - 1 do
    local b = G_MBOX + id * G_MBSIZE
    local w, rd = r.gmem_read(b), r.gmem_read(b + 1)
    if w > rd then
      if w - rd > G_MBQ then rd = w - G_MBQ end
      for k = rd, w - 1 do
        local e = b + 2 + (k % G_MBQ) * 4
        local t, a, c, v = r.gmem_read(e), r.gmem_read(e + 1), r.gmem_read(e + 2), r.gmem_read(e + 3)
        if t == 1 then
          out[#out + 1] = string.format("set %d %d %d", a, c, v)
        elseif t == 2 then
          local tb = G_CTEXT + id * G_CTSIZE
          local n = math.floor(r.gmem_read(tb))
          local chars = {}
          for i = 1, math.min(n, G_CTSIZE - 1) do chars[i] = string.char(math.floor(r.gmem_read(tb + i)) & 255) end
          out[#out + 1] = string.format("comment %d %d %s", a, c, tohex(table.concat(chars)))
        end
      end
      r.gmem_write(b + 1, w)
    end
  end
  append(out)
end

-- names of the tracks with a "21 Channel" JS plug-in bound to a channel
local function reportTracks(now)
  if now - tracksT < 2 then return end
  tracksT = now
  local lines = { "tracks-begin" }
  for i = 0, r.CountTracks(0) - 1 do
    local tr = r.GetTrack(0, i)
    local _, name = r.GetSetMediaTrackInfo_String(tr, "P_NAME", "", false)
    if name ~= "" then
      for fx = 0, r.TrackFX_GetCount(tr) - 1 do
        local _, fxname = r.TrackFX_GetFXName(tr, fx, "")
        if fxname:find("21 Channel", 1, true) and fxname:find("^JS") then
          local slot = math.floor(r.TrackFX_GetParam(tr, fx, 4) + 0.5)
          local ch = math.floor(r.TrackFX_GetParam(tr, fx, 5) + 0.5)
          if slot >= 1 then lines[#lines + 1] = string.format("track %d %d %s", slot, ch, tohex(name)) end
        end
      end
    end
  end
  lines[#lines + 1] = "tracks-end"
  local sig = table.concat(lines, "\n")
  if sig ~= lastTracks then
    lastTracks = sig
    append(lines)
  end
end

local function tick()
  local now = r.time_precise()
  r.SetExtState("Remote21", "bridge", token, false)
  r.SetExtState("Remote21", "bridgeT", tostring(now), false)
  beat = beat + 1
  r.gmem_write(G_MAGIC, MAGIC)
  r.gmem_write(G_BEAT, beat)

  -- tell the app that the bridge runs
  if now - lastClientBeat > 1 then
    lastClientBeat = now
    local f = io.open(dir .. "client.beat", "wb")
    if f then f:write(tostring(os.time())); f:close() end
  end

  -- app -> plug-ins
  local s = readFile(dir .. "state.txt")
  local seq = s and s:match("seq (%d+)")
  if seq and seq ~= lastSeq then
    lastSeq, seqT = seq, now
    applyState(s)
  end
  local appAlive = seq ~= nil and now - seqT < 3
  r.gmem_write(G_APP, appAlive and 1 or 0)

  -- plug-ins -> app
  if appAlive then
    readMailboxes()
    reportTracks(now)
  end

  -- a plug-in is open but the app does not answer: start it in the background
  local want = r.gmem_read(G_WANT)
  if want ~= lastWant then lastWant, wantT = want, now end
  if not appAlive and now - wantT < 2 and now - lastLaunch > 20 then
    lastLaunch = now
    local f = io.open(appDir .. "launching", "wb")
    if f then f:write(tostring(os.time())); f:close() end
    os.execute('/usr/bin/open -g -b local.remote21.app --args --background >/dev/null 2>&1 &')
  end

  r.defer(tick)
end

tick()
