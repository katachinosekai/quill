%global debug_package %{nil}

Name:           quill
Version:        0.1.3
Release:        1%{?dist}
Summary:        Desktop app for Claude Code sessions
License:        MIT AND BSD-3-Clause AND (MIT-0 OR Unlicense)
URL:            https://github.com/katachinosekai/quill
Source0:        %{name}-%{version}.tar.gz
ExclusiveArch:  x86_64

BuildRequires:  golang >= 1.26
BuildRequires:  gcc
BuildRequires:  gcc-c++
BuildRequires:  cmake
BuildRequires:  make
BuildRequires:  desktop-file-utils
BuildRequires:  mesa-libGL-devel
BuildRequires:  libX11-devel
BuildRequires:  libXcursor-devel
BuildRequires:  libXrandr-devel
BuildRequires:  libXinerama-devel
BuildRequires:  libXi-devel
BuildRequires:  libXxf86vm-devel
BuildRequires:  wayland-devel
BuildRequires:  libxkbcommon-devel

%description
Quill is a desktop app for Claude Code sessions. It runs the claude CLI in
the background and gives each session a chat window, a session sidebar,
approvals, live updates and built-in offline speech to text. Requires the
claude CLI to be installed separately.

%prep
%autosetup
cp third_party/fyne/LICENSE LICENSE.fyne
cp third_party/whisper.cpp/LICENSE LICENSE.whisper.cpp
cp vendor/github.com/gen2brain/malgo/LICENSE LICENSE.malgo
cp vendor/rsc.io/qr/LICENSE LICENSE.qr

%build
export GOFLAGS="-mod=vendor -trimpath"
export CGO_ENABLED=1
sh scripts/build-whisper.sh
go build -ldflags "-X main.version=%{version}" -tags wayland -o quill-wayland ./cmd/quill
go build -ldflags "-X main.version=%{version}" -o quill-x11 ./cmd/quill

%install
install -Dm755 quill-wayland %{buildroot}%{_libexecdir}/quill/quill-wayland
install -Dm755 quill-x11 %{buildroot}%{_libexecdir}/quill/quill-x11
install -Dm755 packaging/quill.sh %{buildroot}%{_bindir}/quill
install -Dm644 packaging/io.github.katachinosekai.quill.desktop %{buildroot}%{_datadir}/applications/io.github.katachinosekai.quill.desktop
install -Dm644 cmd/quill/Icon.png %{buildroot}%{_datadir}/icons/hicolor/256x256/apps/io.github.katachinosekai.quill.png
install -Dm644 cmd/quill/icon.svg %{buildroot}%{_datadir}/icons/hicolor/scalable/apps/io.github.katachinosekai.quill.svg

%check
desktop-file-validate %{buildroot}%{_datadir}/applications/io.github.katachinosekai.quill.desktop

%files
%license LICENSE LICENSE.fyne LICENSE.whisper.cpp LICENSE.malgo LICENSE.qr
%doc README.md
%{_bindir}/quill
%{_libexecdir}/quill/
%{_datadir}/applications/io.github.katachinosekai.quill.desktop
%{_datadir}/icons/hicolor/256x256/apps/io.github.katachinosekai.quill.png
%{_datadir}/icons/hicolor/scalable/apps/io.github.katachinosekai.quill.svg

%changelog
* Thu Nov 19 2026 katachinosekai <74618991+katachinosekai@users.noreply.github.com> - 0.1.3-1
- Plan-first approval fixes, queued messages, compact chat, security update (golang.org/x/image 0.43)

* Thu Nov 19 2026 katachinosekai <74618991+katachinosekai@users.noreply.github.com> - 0.1.2-1
- Signed self-updater, split view message boxes, tab shortcuts, crash reporter, security fixes

* Thu Nov 19 2026 katachinosekai <74618991+katachinosekai@users.noreply.github.com> - 0.1.1-1
- Phone folder sync for Quill for Android (pairing QR, local network, synced file, secret gist); tabs, split view and layout updates

* Thu Nov 19 2026 katachinosekai <74618991+katachinosekai@users.noreply.github.com> - 0.1.0-1
- First packaged release
