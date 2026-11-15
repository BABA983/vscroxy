package request

import (
	"fmt"
	"strings"
)

// objcSnippet writes an NSURLSession request with a delegate that stops
// redirects, matching the app's own sends.
func objcSnippet(method string, req *Prepared, headers []snippetHeader) (string, error) {
	if err := requireASCIIHeaders("Objective-C", headers); err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString("// Objective-C with Foundation; build with -framework Foundation.\n")
	b.WriteString("#import <Foundation/Foundation.h>\n\n")
	b.WriteString("@interface NoRedirects : NSObject <NSURLSessionTaskDelegate>\n@end\n\n")
	b.WriteString("@implementation NoRedirects\n")
	b.WriteString("- (void)URLSession:(NSURLSession *)session task:(NSURLSessionTask *)task\n")
	b.WriteString("    willPerformHTTPRedirection:(NSHTTPURLResponse *)response newRequest:(NSURLRequest *)request\n")
	b.WriteString("    completionHandler:(void (^)(NSURLRequest *))completionHandler {\n")
	b.WriteString("    completionHandler(nil);\n")
	b.WriteString("}\n@end\n\n")
	b.WriteString("int main(int argc, const char *argv[]) {\n")
	b.WriteString("    @autoreleasepool {\n")
	b.WriteString("        NSURL *url = [NSURL URLWithString:" + objcQuote(req.URL) + "];\n")
	b.WriteString("        NSMutableURLRequest *request = [NSMutableURLRequest requestWithURL:url];\n")
	b.WriteString("        request.HTTPMethod = " + objcQuote(method) + ";\n")
	for _, h := range headers {
		if strings.EqualFold(h.name, "Host") {
			return "", fmt.Errorf("Objective-C NSURLSession cannot set the Host header; copy it as cURL instead")
		}
		values, err := snippetHeaderValues(h, "Objective-C")
		if err != nil {
			return "", err
		}
		for _, value := range values {
			b.WriteString("        [request addValue:" + objcQuote(value) + " forHTTPHeaderField:" + objcQuote(h.name) + "];\n")
		}
	}
	switch {
	case req.BodyFile != "":
		b.WriteString("        request.HTTPBody = [NSData dataWithContentsOfFile:" + objcQuote(req.BodyFile) + "];\n")
	case req.Body != "":
		b.WriteString("        request.HTTPBody = [" + objcQuote(req.Body) + " dataUsingEncoding:NSUTF8StringEncoding];\n")
	}
	b.WriteString("        NSURLSessionConfiguration *config = [NSURLSessionConfiguration defaultSessionConfiguration];\n")
	b.WriteString("        NoRedirects *delegate = [NoRedirects new];\n")
	b.WriteString("        NSURLSession *session = [NSURLSession sessionWithConfiguration:config delegate:delegate delegateQueue:nil];\n")
	b.WriteString("        dispatch_semaphore_t done = dispatch_semaphore_create(0);\n")
	b.WriteString("        [[session dataTaskWithRequest:request completionHandler:^(NSData *data, NSURLResponse *response, NSError *error) {\n")
	b.WriteString("            if (error) {\n")
	b.WriteString("                NSLog(@\"%@\", error);\n")
	b.WriteString("            } else {\n")
	b.WriteString("                NSLog(@\"%ld\", (long)((NSHTTPURLResponse *)response).statusCode);\n")
	b.WriteString("                NSLog(@\"%@\", [[NSString alloc] initWithData:data encoding:NSUTF8StringEncoding]);\n")
	b.WriteString("            }\n")
	b.WriteString("            dispatch_semaphore_signal(done);\n")
	b.WriteString("        }] resume];\n")
	b.WriteString("        dispatch_semaphore_wait(done, DISPATCH_TIME_FOREVER);\n")
	b.WriteString("        [session finishTasksAndInvalidate];\n")
	b.WriteString("    }\n")
	b.WriteString("    return 0;\n")
	b.WriteString("}\n")
	return b.String(), nil
}

// objcQuote writes s as an Objective-C @"..." literal. Non-ASCII stays as
// UTF-8 in the source, which Clang accepts for NSString literals.
func objcQuote(s string) string {
	var b strings.Builder
	b.WriteString("@\"")
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			switch {
			case r < 0x20 || r == 0x7f:
				fmt.Fprintf(&b, `\%03o`, r)
			default:
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}
