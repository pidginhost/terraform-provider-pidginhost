# Import a record by "<zone>/<source>/<name>/<type>", when the zone has one
# record of that name and type. Write the name as the configuration does.
terraform import pidginhost_dns_record.www example.com/internal/www/A

# Otherwise add the record's values in zone file order, separated by spaces.
terraform import pidginhost_dns_record.mail "example.com/internal/@/MX/10 mail.example.com"
