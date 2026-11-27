// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 The YACM Authors. Additional terms under GPLv3 section 7: see NOTICE.md.

// What's new, shown once after an update and from Settings > About, and the
// changes in the release notes (build/release-notes.mjs). Every version from
// 0.9.1 on has an entry. Newest first; short, user-facing lines, the user
// guide has the details:
//   { version: '1.1.0', items: [['Title', 'One or two sentences.'], …] },
export const WHATS_NEW = [
  {
    version: '1.2.0',
    items: [
      ['Edit many sessions in the editor', 'Select sessions or a group and edit them in the same editor as one session: every setting, with a mark on what you change. Lists such as automation rules, saved commands and tags can be added to each session, replaced or cleaned of matching entries. Names and addresses stay each session\'s own.'],
      ['Folders that stay sorted', 'Right click a folder > Sort by: name (r2 before r10), address, connection type, opened last or size. New and renamed sessions take their place; subfolders can follow.'],
      ['Local terminals and containers', 'A session of type Local opens PowerShell, cmd, WSL, bash or zsh on this computer, or a shell in a Docker container or Kubernetes pod, with logs, saved commands and automation like any session. Right click below the tree > Open a local terminal.'],
      ['Jump hosts that do not forward', 'A router or a server without TCP forwarding can still be the jump host: YACM logs in to it, types ssh or telnet (or your own command, such as telnet <HOST> /vrf MGMT) and answers the device\'s login.'],
      ['IPMI Serial-over-LAN', 'A server\'s serial console through its BMC (iDRAC, iLO, any IPMI 2.0 BMC), without the vendor\'s web page or Java.'],
      ['Passwords from KeePass or Bitwarden', 'A credential profile can read its password from a KeePass database or a Bitwarden vault when you connect, instead of keeping a copy.'],
      ['Linux with NVIDIA', 'The window no longer stays blank with the NVIDIA driver: YACM turns off the WebKit renderer that fails there.'],
      ['Session editor', 'The connection type is a list; the user name is under Authentication, next to the password and the credential profile.'],
      ['Type to many', 'Only the command field sends to the checked sessions: what you type or paste in a terminal, and saved commands, go to that one session. The field has a row of its own and the sessions wrap above it, so it stays usable with many sessions; the label counts the checked ones (5 of 30).'],
    ],
  },
  {
    version: '1.1.0',
    items: [
      ['Royal TS import', 'Sessions, folders, credentials and saved passwords from Royal TS and Royal TSX documents (.rtsz), also password-protected ones.'],
      ['Choose what to import', 'An import from another program lets you leave out passwords, credential profiles, automation rules or jump hosts. On a fresh install, the session list offers a button for each program whose sessions are found.'],
      ['Cut, copy and paste in the session list', 'Ctrl+X, Ctrl+C and Ctrl+V move or copy sessions and groups, saved passwords included. Edit several sessions together now also switches them between telnet and SSH.'],
      ['Lock that guards open sessions', 'Settings > Security can make the lock screen ask for the master password before open sessions can be used again, or close them. While YACM is locked, nothing typed reaches a session.'],
      ['Imports of .yacm files', 'Exports can leave out user names and logins, for handing sessions to a colleague. Your own export brings all settings to a new computer; sessions that would log in with one of your profiles keep it only when you allow it.'],
      ['Copy whole folders', 'The Files tab copies folders with everything in them, goes on with a stopped SFTP transfer (Resume), and offers Send with sudo… when a Linux server refuses to write a file there.'],
      ['File servers', 'SFTP on the SCP server, and on Linux ports below 1024 (TFTP 69) for YACM alone after one password prompt. The servers reach only their shared folder.'],
      ['Very old devices', 'The Legacy algorithm set accepts RSA host keys shorter than 1024 bits (768-bit keys of old gear); the host key question shows the size of an RSA key.'],
      ['Tabs', 'Tabs keep a readable width and long names are shortened in the middle (in the session list too); a second tab of a session is numbered, the mouse wheel scrolls the tab bar, new output marks a tab that is not on screen, and the tab menu can close all tabs.'],
      ['Session list', 'Ctrl + and Ctrl - in the session list make it larger or smaller; a middle click in the terminal can paste (Settings > Terminal).'],
      ['Team libraries', 'One session with a bad entry is left out (and named in Settings) instead of the whole library; automation can answer sudo\'s password prompt with your own login.'],
      ['Files tab', 'A device that does not answer the request for a file channel no longer leaves the Files tab waiting: after 30 seconds YACM opens a new connection and tries again.'],
      ['Session logs', 'The SNMP community in an ASA snmp-server host line is masked.'],
      ['Prompts', 'A device name with characters such as + in its prompt (RU_ZG_IS+C_JUG#) is recognised: anti-idle works there and the name is offered for the session.'],
    ],
  },
  {
    version: '1.0.0',
    items: [
      ['YACM 1.0', 'The first stable release, after the public beta. Thank you to everyone who tested it and reported problems.'],
      ['Session names from the prompt', 'YACM offers the device\'s hostname only from the first prompt within 30 seconds of connecting, so a device or shell you go on to from the session is never offered as its name.'],
      ['Log file names', 'Settings and the session editor list the {stamp} token (20261113-103743); {name}_{stamp}.txt keeps the default name with a .txt ending.'],
      ['Microsoft Store', 'The Store version needs Windows 11. It keeps its data in %AppData%\\YACM, shared with the zip version, which runs on Windows 10 and 11.'],
    ],
  },
  {
    version: '0.9.3',
    items: [
      ['Microsoft Store', 'YACM for Windows comes as a Microsoft Store package too, which the Store keeps up to date. It uses the same data folder as the zip version, so sessions and the vault stay where they are.'],
      ['New project address', 'The project moved to github.com/yacm-app/yacm; the old address forwards there. Homebrew: brew install --cask yacm-app/tap/yacm.'],
    ],
  },
  {
    version: '0.9.2',
    items: [
      ['Linux packages', '.deb (Ubuntu 22.04+, Debian 12+) and .rpm (Fedora) add the signed package repository pkg.yacm.app, so apt upgrade and dnf upgrade bring new versions.'],
      ['Safer .yacm import', 'Agent forwarding, outbound proxies and log folders from someone else\'s file are left out, and the import report says so. Imported host key lines are checked like the trusted host keys file, and one unreadable line there no longer switches off the whole file.'],
      ['Session logs', 'Logs and output files keep UTF-8 text whole (Czech, Romanian, Cyrillic, CJK).'],
      ['Vault', 'A master password change that cannot be saved keeps the old password.'],
      ['File servers', 'Uploads are limited (16 GiB, at least 1 GiB of free space kept); stopping the SCP server also ends its open connections.'],
      ['NetBox', 'Only https (http just for this computer); the saved token goes only to the address it was saved for.'],
      ['Third-party licences', 'In every package and in Settings > About.'],
    ],
  },
  {
    version: '0.9.1',
    items: [
      ['File transfers', 'A transfer started from a terminal tab goes on when the device logs the terminal out, and shows its speed and the time left. An SCP transfer that Cisco IOS closes as it starts is tried again.'],
      ['SecureCRT import', 'Credential sets are found also when you import a folder inside Sessions.'],
      ['Terminal', 'Shift+Insert pastes once; Ctrl+Shift+Tab goes to the previous tab on Linux too.'],
      ['Session logs', 'The SNMP community in snmp-server host and a server key given without a type are masked.'],
      ['Vault backups', 'After a master password change the backups are encrypted with the new password, so the old one opens none of them.'],
      ['File servers', 'The TFTP server takes at most 32 transfers at a time.'],
      ['Releases', 'Every release has its build provenance attached as a file, for checking the packages offline.'],
    ],
  },
];

// cmp compares the numbers of two versions; a release candidate (1.1.0-rc.1)
// counts as its version, so its testers see that version's notes.
const cmp = (a, b) => {
  const nums = v => String(v).split('-')[0].split('.').map(Number);
  const pa = nums(a), pb = nums(b);
  for (let i = 0; i < 3; i++) { if ((pa[i] || 0) !== (pb[i] || 0)) return (pa[i] || 0) - (pb[i] || 0); }
  return 0;
};

// whatsNewFor lists the entries new since `since` (or just the current
// version), at most two versions back.
export function whatsNewFor(current, since = '') {
  return WHATS_NEW.filter(v => cmp(v.version, current) <= 0 && (!since || cmp(v.version, since) > 0)).slice(0, 2);
}
