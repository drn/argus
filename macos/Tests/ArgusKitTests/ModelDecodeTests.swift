import Foundation
import Testing
@testable import ArgusKit

@Suite("Model decoding")
struct ModelDecodeTests {
    let decoder = JSONDecoder()

    private func decode<T: Decodable>(_ type: T.Type, _ json: String) throws -> T {
        try decoder.decode(T.self, from: Data(json.utf8))
    }

    // Shape: internal/api/handlers.go taskToJSON / taskJSON. omitempty fields
    // (idle, needs_input, branch, backend, elapsed, archived, worktree_path,
    // prompt, pr_state) are dropped when falsy/empty.
    @Test("Task decodes SPA shape with omitempty fields absent")
    func taskMinimal() throws {
        let json = """
        {"id":"1700000000000","name":"fix bug","status":"in_progress",
         "project":"argus","created_at":"2026-07-02T12:00:00Z"}
        """
        let t = try decode(Task.self, json)
        #expect(t.id == "1700000000000")
        #expect(t.name == "fix bug")
        #expect(t.status == "in_progress")
        #expect(t.taskStatus == .inProgress)
        #expect(t.project == "argus")
        #expect(t.idle == false)
        #expect(t.needsInput == false)
        #expect(t.archived == false)
        #expect(t.branch == nil)
        #expect(t.prState == nil)
    }

    @Test("Task decodes with all runtime + omitempty fields present")
    func taskFull() throws {
        let json = """
        {"id":"1","name":"t","status":"in_progress","idle":true,"needs_input":true,
         "project":"argus","branch":"argus/t","backend":"claude","elapsed":"5m",
         "created_at":"2026-07-02T12:00:00Z","archived":true,
         "worktree_path":"/w/t","prompt":"do it","pr_state":"awaiting-review"}
        """
        let t = try decode(Task.self, json)
        #expect(t.idle == true)
        #expect(t.needsInput == true)
        #expect(t.branch == "argus/t")
        #expect(t.backend == "claude")
        #expect(t.elapsed == "5m")
        #expect(t.archived == true)
        #expect(t.worktreePath == "/w/t")
        #expect(t.prompt == "do it")
        #expect(t.prState == "awaiting-review")
    }

    // Shape: internal/api/handlers.go handleCreateTask response.
    @Test("CreateTaskResponse decodes")
    func createTaskResp() throws {
        let t = try decode(CreateTaskResponse.self, #"{"id":"9","name":"n","status":"in_progress"}"#)
        #expect(t.id == "9")
        #expect(t.status == "in_progress")
    }

    // Shape: internal/api/handlers.go handleResumeTask / handleRestartTask.
    @Test("SessionActionResult decodes with optional healed")
    func sessionAction() throws {
        let a = try decode(SessionActionResult.self, #"{"status":"resumed","pid":4242}"#)
        #expect(a.status == "resumed")
        #expect(a.pid == 4242)
        #expect(a.healed == false)
        let b = try decode(SessionActionResult.self, #"{"status":"restarted","pid":1,"healed":true}"#)
        #expect(b.healed == true)
    }

    // Shape: internal/api/hera.go heraJSON / heraOrchJSON / heraRoleJSON.
    @Test("HeraRoster decodes orchestrators and freelance roles")
    func heraRoster() throws {
        let json = """
        {"orchestrators":[
          {"id":1,"name":"build","pinned":true,"archived":false,"roles":[
            {"role_id":10,"orch_id":1,"name":"coord","kind":"coordinator",
             "status":"working","task_id":"t1","task_name":"Coordinate","task_status":"in_progress",
             "live":true,"ready_to_close":false,"archived":false}
          ]}
        ],
        "freelance":[
          {"role_id":20,"orch_id":2,"name":"free","kind":"freelance","status":"",
           "task_id":"","task_name":"","task_status":"","live":false,
           "ready_to_close":false,"archived":false}
        ]}
        """
        let r = try decode(HeraRoster.self, json)
        #expect(r.orchestrators.count == 1)
        #expect(r.orchestrators[0].pinned == true)
        let role = r.orchestrators[0].roles[0]
        #expect(role.roleID == 10)
        #expect(role.kind == "coordinator")
        #expect(role.live == true)
        #expect(r.freelance.count == 1)
        #expect(r.freelance[0].kind == "freelance")
    }

    // Shape: internal/api/schedules.go scheduleJSON.
    @Test("Schedule decodes with omitempty timestamps optional")
    func schedule() throws {
        let json = """
        {"id":"s1","name":"nightly","project":"argus","prompt":"run",
         "schedule":"0 2 * * *","enabled":true,"created_at":"2026-07-01T00:00:00Z",
         "next_run_at":"2026-07-03T02:00:00Z"}
        """
        let s = try decode(Schedule.self, json)
        #expect(s.id == "s1")
        #expect(s.schedule == "0 2 * * *")
        #expect(s.enabled == true)
        #expect(s.nextRunAt == "2026-07-03T02:00:00Z")
        #expect(s.runOnceAt == nil)
        #expect(s.lastError == nil)
    }

    // Shape: internal/api/settings.go settingsResponse.
    @Test("Settings decodes all sections")
    func settings() throws {
        let json = """
        {"sandbox":{"enabled":true,"available":true,"deny_read":["/etc"],
          "extra_write":[],"allow_apple_events":["com.apple.mail"]},
         "kb":{"enabled":false,"metis_vault_path":"/vault"},
         "api":{"enabled":true,"http_port":7743},
         "defaults":{"backend":"claude","share_project":"argus","permission_mode":"bypassPermissions"}}
        """
        let s = try decode(Settings.self, json)
        #expect(s.sandbox.enabled == true)
        #expect(s.sandbox.denyRead == ["/etc"])
        #expect(s.sandbox.extraWrite == [])
        #expect(s.sandbox.allowAppleEvents == ["com.apple.mail"])
        #expect(s.api.httpPort == 7743)
        #expect(s.defaults.permissionMode == "bypassPermissions")
        #expect(s.kb.metisVaultPath == "/vault")
    }

    // Shape: internal/api/metrics.go systemMetricsResponse (sysmetrics.Snapshot
    // flattened + sessions).
    @Test("SystemMetrics decodes flattened snapshot + sessions")
    func systemMetrics() throws {
        let json = """
        {"cpu_percent":12.5,"cpu_avail":true,
         "load1":1.2,"load5":1.1,"load15":0.9,"load_avail":true,
         "mem_total":34359738368,"mem_used":17179869184,"mem_available":17179869184,
         "mem_percent":50.0,"mem_avail":true,
         "swap_total":0,"swap_used":0,"swap_percent":0,"swap_avail":false,
         "disk_total":1000,"disk_used":400,"disk_free":600,"disk_percent":40.0,
         "disk_path":"/","disk_avail":true,
         "proc_rss":123456,"proc_avail":true,
         "uptime_sec":86400,"uptime_avail":true,
         "sampled_at":"2026-07-02T12:00:00Z",
         "sessions":{"running":2,"idle":1}}
        """
        let m = try decode(SystemMetrics.self, json)
        #expect(m.cpuPercent == 12.5)
        #expect(m.memTotal == 34359738368)
        #expect(m.swapAvail == false)
        #expect(m.diskPath == "/")
        #expect(m.uptimeSec == 86400)
        #expect(m.sessions.running == 2)
        #expect(m.sessions.idle == 1)
    }

    // Shape: internal/api/handlers.go handleStatus statusResponse.
    @Test("DaemonStatus decodes counts")
    func daemonStatus() throws {
        let json = """
        {"ok":true,"sessions":{"running":3,"idle":2},
         "tasks":{"pending":1,"in_progress":3,"in_review":2,"complete":10}}
        """
        let s = try decode(DaemonStatus.self, json)
        #expect(s.ok == true)
        #expect(s.sessions.running == 3)
        #expect(s.tasks.inProgress == 3)
        #expect(s.tasks.complete == 10)
    }

    // Shape: internal/gitutil/types.go GitStatusRefreshMsg — NO json tags, so
    // Go emits PascalCase field names.
    @Test("GitStatus decodes PascalCase keys")
    func gitStatus() throws {
        let json = """
        {"TaskID":"t1","Status":" M file.go","Diff":"1 file changed",
         "BranchDiff":"2 files","BranchFiles":"M\\tfile.go"}
        """
        let g = try decode(GitStatus.self, json)
        #expect(g.taskID == "t1")
        #expect(g.status == " M file.go")
        #expect(g.branchDiff == "2 files")
        #expect(g.branchFiles == "M\tfile.go")
    }

    // Shape: internal/gitutil/types.go DirFilesMsg — Files is null when empty.
    @Test("FileTree decodes with null Files as empty array")
    func fileTreeNullFiles() throws {
        let f = try decode(FileTree.self, #"{"TaskID":"t1","DirPath":"","Files":null}"#)
        #expect(f.files.isEmpty)
    }

    @Test("FileTree decodes ChangedFile entries")
    func fileTreeEntries() throws {
        let json = """
        {"TaskID":"t1","DirPath":"src","Files":[
          {"Status":"M","Path":"src/a.go","IsDir":false},
          {"Status":"??","Path":"src/dir","IsDir":true}]}
        """
        let f = try decode(FileTree.self, json)
        #expect(f.files.count == 2)
        #expect(f.files[0].status == "M")
        #expect(f.files[1].isDir == true)
    }

    // Shape: internal/links/links.go Link (json:"isPR").
    @Test("Link decodes isPR key")
    func link() throws {
        let l = try decode(Link.self,
                           #"{"label":"PR #1","url":"https://github.com/o/r/pull/1","isPR":true}"#)
        #expect(l.isPR == true)
        #expect(l.url.contains("/pull/1"))
    }

    // Shape: model.TaskMessage as returned in the inbox "messages" array.
    @Test("Inbox + InboxMessage decode; read_at optional")
    func inbox() throws {
        let json = """
        {"messages":[
          {"id":"m1","from":"t1","to":"t2","kind":"note","body":"hi",
           "created_at":"2026-07-02T12:00:00Z"},
          {"id":"m2","from":"t1","to":"t2","kind":"answer","body":"done",
           "in_reply_to":"m1","created_at":"2026-07-02T12:05:00Z",
           "read_at":"2026-07-02T12:06:00Z"}
        ],"unread_count":1}
        """
        let inbox = try decode(Inbox.self, json)
        #expect(inbox.unreadCount == 1)
        #expect(inbox.messages.count == 2)
        #expect(inbox.messages[0].readAt == nil)
        #expect(inbox.messages[0].isUnread == true)
        #expect(inbox.messages[1].inReplyTo == "m1")
        #expect(inbox.messages[1].isUnread == false)
    }

    // Shape: internal/api/task_meta.go metaEntryJSON.
    @Test("MetaEntry decodes")
    func metaEntry() throws {
        let json = """
        {"namespace":"pr","key":"state","value":"awaiting-review",
         "updated_at":"2026-07-02T12:00:00Z"}
        """
        let e = try decode(MetaEntry.self, json)
        #expect(e.namespace == "pr")
        #expect(e.key == "state")
        #expect(e.value == "awaiting-review")
    }

    // Shape: internal/claudesession.Session via internal/api's claude-sessions
    // handler (see specs/rest-api/spec.md). mod_time is a Go time.Time,
    // JSON-marshaled as RFC3339 with a variable-length (possibly absent)
    // fractional-second component.
    @Test("ClaudeSession decodes with a zero-fractional mod_time")
    func claudeSessionZeroFractional() throws {
        let json = """
        {"id":"abc-123","title":"fix the thing","branch":"argus/fix",
         "pr_ref":"o/r#42","mod_time":"2026-08-21T10:00:00Z","size_bytes":4096}
        """
        let s = try decode(ClaudeSession.self, json)
        #expect(s.id == "abc-123")
        #expect(s.title == "fix the thing")
        #expect(s.branch == "argus/fix")
        #expect(s.prRef == "o/r#42")
        #expect(s.sizeBytes == 4096)
        var cal = Calendar(identifier: .gregorian)
        cal.timeZone = TimeZone(identifier: "UTC")!
        let comps = cal.dateComponents([.year, .month, .day, .hour, .minute, .second], from: s.modTime)
        #expect(comps.year == 2026 && comps.month == 8 && comps.day == 21)
        #expect(comps.hour == 10 && comps.minute == 0 && comps.second == 0)
    }

    @Test("ClaudeSession decodes a fractional-second mod_time, ignoring sub-second precision")
    func claudeSessionFractional() throws {
        let json = """
        {"id":"abc-124","title":"t","branch":"","pr_ref":"",
         "mod_time":"2026-08-21T10:00:00.123456789Z","size_bytes":0}
        """
        let s = try decode(ClaudeSession.self, json)
        var cal = Calendar(identifier: .gregorian)
        cal.timeZone = TimeZone(identifier: "UTC")!
        let comps = cal.dateComponents([.year, .month, .day, .hour, .minute, .second], from: s.modTime)
        #expect(comps.hour == 10 && comps.minute == 0 && comps.second == 0)
        #expect(s.branch == "")
        #expect(s.prRef == "")
    }

    @Test("ClaudeSession fails to decode an unparseable mod_time")
    func claudeSessionBadModTime() throws {
        let json = """
        {"id":"x","title":"t","branch":"","pr_ref":"","mod_time":"not-a-date","size_bytes":0}
        """
        #expect(throws: (any Error).self) {
            try decode(ClaudeSession.self, json)
        }
    }

    // Shape: GET /api/tasks/{id}/claude-sessions response envelope.
    @Test("ClaudeSessionsResponse decodes sessions + current_session_id")
    func claudeSessionsResponse() throws {
        let json = """
        {"sessions":[
          {"id":"s1","title":"newest","branch":"b1","pr_ref":"",
           "mod_time":"2026-08-21T10:00:00Z","size_bytes":100},
          {"id":"s2","title":"older","branch":"b2","pr_ref":"o/r#1",
           "mod_time":"2026-08-20T10:00:00Z","size_bytes":200}
        ],"current_session_id":"s1"}
        """
        let r = try decode(ClaudeSessionsResponse.self, json)
        #expect(r.sessions.count == 2)
        #expect(r.sessions[0].id == "s1")
        #expect(r.currentSessionID == "s1")
    }

    @Test("ClaudeSessionsResponse decodes an empty current_session_id")
    func claudeSessionsResponseEmptyCurrent() throws {
        let r = try decode(ClaudeSessionsResponse.self, #"{"sessions":[],"current_session_id":""}"#)
        #expect(r.sessions.isEmpty)
        #expect(r.currentSessionID == "")
    }

    // Shape: POST /api/tasks/{id}/claude-session response.
    @Test("ClaudeSessionSwitchResponse decodes the switched shape with pid")
    func claudeSessionSwitchResponseSwitched() throws {
        let r = try decode(ClaudeSessionSwitchResponse.self, #"{"status":"switched","pid":4242}"#)
        #expect(r.status == "switched")
        #expect(r.pid == 4242)
    }

    @Test("ClaudeSessionSwitchResponse decodes the unchanged shape with no pid key")
    func claudeSessionSwitchResponseUnchanged() throws {
        let r = try decode(ClaudeSessionSwitchResponse.self, #"{"status":"unchanged"}"#)
        #expect(r.status == "unchanged")
        #expect(r.pid == nil)
    }

    // Shape: internal/api/handlers.go handleGetConfig returns config.Config.
    @Test("JSONValue decodes an arbitrary config object")
    func jsonValueConfig() throws {
        let json = """
        {"api":{"enabled":true,"http_port":7743},"projects":{"argus":{"path":"/repo"}},
         "count":5,"ratio":0.5,"tags":["a","b"],"nothing":null}
        """
        let v = try decode(JSONValue.self, json)
        #expect(v["api"]?["enabled"]?.boolValue == true)
        #expect(v["api"]?["http_port"]?.intValue == 7743)
        #expect(v["count"]?.intValue == 5)
        #expect(v["ratio"]?.doubleValue == 0.5)
        #expect(v["tags"]?.arrayValue?.count == 2)
        #expect(v["nothing"] == JSONValue.null)
    }
}
