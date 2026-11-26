// Finder entry point: Terminal owns the interactive session and its lifetime.
#import <AppKit/AppKit.h>

@interface Launcher : NSObject <NSApplicationDelegate>
@end

@implementation Launcher
- (void)fail:(NSString *)message {
    NSAlert *alert = [NSAlert new];
    alert.messageText = @"Не удалось открыть ShadowTalk";
    alert.informativeText = message;
    [alert runModal];
    [NSApp terminate:nil];
}

- (void)applicationDidFinishLaunching:(NSNotification *)notification {
    NSWorkspace *workspace = NSWorkspace.sharedWorkspace;
    NSURL *terminal = [workspace URLForApplicationWithBundleIdentifier:@"com.apple.Terminal"];
    NSURL *script = [NSBundle.mainBundle URLForResource:@"ShadowTalk" withExtension:@"command"];
    if (!terminal || !script) {
        [self fail:@"Проверьте наличие Terminal и целостность ShadowTalk.app."];
        return;
    }
    // Open a file URL, never interpolate a bundle path into shell/AppleScript.
    [workspace openURLs:@[script]
        withApplicationAtURL:terminal
        configuration:[NSWorkspaceOpenConfiguration configuration]
        completionHandler:^(NSRunningApplication *application, NSError *error) {
            dispatch_async(dispatch_get_main_queue(), ^{
                if (error) {
                    [self fail:error.localizedDescription];
                } else {
                    [NSApp terminate:nil];
                }
            });
        }];
}
@end

int main(void) {
    @autoreleasepool {
        [NSApplication sharedApplication];
        Launcher *delegate = [Launcher new];
        NSApp.delegate = delegate;
        [NSApp run];
    }
    return 0;
}
